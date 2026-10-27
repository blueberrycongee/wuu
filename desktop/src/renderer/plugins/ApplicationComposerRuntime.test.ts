import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initialState, type AppState } from "../AppState";
import type { EngineListResult, InitializeResult, Thread } from "../../shared/protocol";
import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { ApplicationComposerRuntime } from "./ApplicationComposerRuntime";

vi.mock("../Toast", () => ({ showErrorToast: vi.fn() }));
const original = window.wuu;
beforeEach(() => localStorage.clear());
afterEach(() => { window.wuu = original; localStorage.clear(); });

const context = { kind: "project" as const, project_id: "one", cwd: "/one" };
function configuration(): InitializeResult {
  return { protocol_version: "test", workspace_root: "/one", provider: "api", model: "a", variant: "high", effort: "high", permissions: { mode: "standard" },
    providers: [{ name: "api", type: "openai", model: "a", models: [{ id: "a", supported_efforts: ["high", "low"] }, { id: "b", supported_efforts: ["low"] }] }] };
}
function fixture(sharedInventory?: () => import("../../../../packages/plugin-sdk/src/application-settings").ApplicationSettingsViewV1["engineInventory"]) {
  let state: AppState = { ...initialState, activeContext: context, initialized: configuration(), activeSessionTabID: "draft-a",
    sessionTabs: ["draft-a", "draft-b"].map(id => ({ id, kind: "draft", context, title: id, prompt: "", images: [], files: [], createdAt: 1 })),
    gitStatus: { is_repo: true, dirty_count: 0, branches: ["main", "feature"] } };
  const changed = vi.fn();
  const runtime = new ApplicationComposerRuntime({ getState: () => state, updateState: update => { state = typeof update === "function" ? update(state) : update; }, changed, isNavigationPending: () => false, getEngineInventory: sharedInventory });
  return { runtime, changed, get: () => state, set: (next: AppState) => { state = next; }, view: (id = state.activeSessionTabID) => runtime.getSnapshot().owners.find(row => row.ownerTabId === id)! };
}
const inventory: EngineListResult = { settings: { default_engine: "wuu" }, engines: [{ id: "wuu", enabled: true, binary_ok: true }, { id: "native", enabled: true, binary_ok: true, models: [
  { id: "native-default", is_default: true, default_effort: "medium", default_speed: "fast", supported_efforts: ["low", "medium"], options: [{ id: "mode", label: "Mode", type: "select", default_value: "normal", choices: [{ value: "normal", label: "Normal" }] }] },
  { id: "other", supported_efforts: ["high"] },
] }] };
function installEngineAPI() { window.wuu = { listEngines: vi.fn().mockResolvedValue(inventory) } as unknown as typeof window.wuu; }
function openedThread(h: ReturnType<typeof fixture>, overrides: Partial<Thread> = {}) {
  const thread: Thread = { id: "thread-one", preview: "", cwd: "/one", model_provider: "api", model: "a", engine_id: "wuu", status: "idle", created_at: "", updated_at: "", turns: [], ...overrides };
  h.set({ ...h.get(), thread, threads: [thread], activeSessionTabID: "thread:thread-one", sessionTabs: [{ id: "thread:thread-one", kind: "thread", threadID: thread.id, context, title: "Thread", prompt: "", images: [], files: [] }] });
  return thread;
}

it("retains independent draft choices without changing workspace defaults", async () => {
  installEngineAPI(); const h = fixture(); h.runtime.getSnapshot();
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "provider-model", provider: "api", model: "b", variant: "low" } });
  expect(h.view().selection).toMatchObject({ provider: "api", model: "b", variant: "low" });
  expect(h.get().initialized?.model).toBe("a");
  h.set({ ...h.get(), activeSessionTabID: "draft-b" });
  expect(h.view().selection.model).toBe("a");
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-b", selection: { kind: "provider-model", provider: "api", model: "b", variant: "" } });
  expect(h.view().selection.variant).toBe("");
  h.set({ ...h.get(), activeSessionTabID: "draft-a" });
  expect(h.view().selection.model).toBe("b");
});

it("keeps native defaults separate and remembers child choices per engine", async () => {
  installEngineAPI(); const h = fixture();
  await h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "native" } });
  expect(h.view().selection).toMatchObject({ engine: "native", model: "", effort: "", speed: "", permissionMode: "unconfined" });
  expect(h.view().nativeDefaults).toMatchObject({ model: "native-default", effort: "medium", speed: "fast", modelOptions: { mode: "normal" } });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "native-model", model: "other", effort: "high" } });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "wuu" } });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "native" } });
  expect(h.view().selection).toMatchObject({ model: "other", effort: "high" });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "native-model", model: "", effort: "" } });
  expect(h.view().selection.model).toBe("");
});

it("fences late discovery after tab navigation and after cancellation", async () => {
  const pending: Array<(result: EngineListResult) => void> = [];
  window.wuu = { listEngines: vi.fn(() => new Promise(resolve => pending.push(resolve))) } as unknown as typeof window.wuu;
  const h = fixture(); const first = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  h.set({ ...h.get(), activeSessionTabID: "draft-b" });
  pending[0](inventory); await first;
  expect(h.view().engines).toHaveLength(0);
  const abort = new AbortController(); const second = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-b" }, abort.signal);
  abort.abort(); pending[1](inventory); await expect(second).rejects.toThrow();
  expect(h.view().engines).toHaveLength(0);
  expect(h.view().engineModelsLoading).toBe(false);
});

it("deduplicates discovery and lets a forced refresh supersede an older result", async () => {
  const pending: Array<(result: EngineListResult) => void> = [];
  window.wuu = { listEngines: vi.fn(() => new Promise(resolve => pending.push(resolve))) } as unknown as typeof window.wuu;
  const h = fixture(); const first = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  const same = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  expect(window.wuu.listEngines).toHaveBeenCalledTimes(1);
  const fresh = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a", force: true });
  pending[1]({ engines: [{ id: "new", enabled: true, binary_ok: true }] }); await fresh;
  pending[0](inventory); await Promise.all([first, same]);
  expect(h.view().engines.map(engine => engine.id)).toEqual(["new"]);
  expect(window.wuu.listEngines).toHaveBeenLastCalledWith({ cwd: "/one", refresh_models: true }, context);
});

it("refuses stale owners, bound-engine changes and read-only runtime changes", async () => {
  installEngineAPI(); const h = fixture();
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-b", selection: { kind: "speed", speed: "fast" } })).rejects.toBeInstanceOf(PluginLifecycleCancellationError);
  openedThread(h);
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "thread:thread-one", selection: { kind: "engine", engine: "native" } })).resolves.toEqual({ accepted: false });
  openedThread(h, { read_only: true });
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "thread:thread-one", selection: { kind: "speed", speed: "fast" } })).resolves.toEqual({ accepted: false });
});

it("reuses ordered thread updates and does not apply a late response to a successor draft", async () => {
  const responses: Array<(value: unknown) => void> = [];
  window.wuu = { updateRuntimeSettings: vi.fn(() => new Promise(resolve => responses.push(resolve))) } as unknown as typeof window.wuu;
  const h = fixture(); openedThread(h);
  const first = h.runtime.invoke("composer.choose-runtime", { ownerTabId: "thread:thread-one", selection: { kind: "speed", speed: "fast" } });
  const second = h.runtime.invoke("composer.choose-runtime", { ownerTabId: "thread:thread-one", selection: { kind: "speed", speed: "" } });
  expect(window.wuu.updateRuntimeSettings).toHaveBeenCalledTimes(1);
  responses[0]({ provider: "api", model: "a" }); await first; await Promise.resolve();
  expect(window.wuu.updateRuntimeSettings).toHaveBeenCalledTimes(2);
  h.set({ ...h.get(), thread: undefined, activeSessionTabID: "draft-new", sessionTabs: [...h.get().sessionTabs, { id: "draft-new", kind: "draft", context, title: "New", prompt: "", images: [], files: [], createdAt: 1 }] });
  responses[1]({ provider: "api", model: "wrong-default" }); await second;
  expect(h.get().initialized?.model).toBe("a");
  expect(h.get().threads[0].speed).toBe("");
});

it("scopes permission and worktree decisions to the draft owner", async () => {
  installEngineAPI(); const h = fixture(); h.runtime.getSnapshot();
  await h.runtime.invoke("composer.choose-permission", { ownerTabId: "draft-a", mode: "read_only", approveForMe: true });
  await h.runtime.invoke("composer.choose-worktree", { ownerTabId: "draft-a", enabled: true, startBranch: "feature" });
  expect(h.view().selection).toMatchObject({ permissionMode: "read_only", approveForMe: true });
  expect(h.view().worktree).toMatchObject({ enabled: true, startBranch: "feature" });
  h.set({ ...h.get(), activeSessionTabID: "draft-b" });
  expect(h.view().selection.permissionMode).toBe("standard");
  expect(h.view().worktree.enabled).toBe(false);
});

it("keeps deeply immutable snapshots stable across unrelated stream updates", () => {
  installEngineAPI(); const h = fixture(); const first = h.runtime.getSnapshot();
  h.set({ ...h.get(), status: "streaming" });
  expect(h.runtime.getSnapshot()).toBe(first);
  expect(Object.isFrozen(first.owners[0].providers[0].models)).toBe(true);
  expect(Object.isFrozen(first.owners[0].selection.modelOptions)).toBe(true);
});

it("discards discoveries for a closed owner and rejects work after disposal", async () => {
  let resolve!: (value: EngineListResult) => void;
  window.wuu = { listEngines: vi.fn(() => new Promise(done => { resolve = done; })) } as unknown as typeof window.wuu;
  const h = fixture(); const request = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  h.set({ ...h.get(), activeSessionTabID: "draft-b", sessionTabs: h.get().sessionTabs.filter(tab => tab.id !== "draft-a") });
  h.runtime.getSnapshot(); resolve(inventory); await request;
  expect(h.runtime.getSnapshot().owners).toHaveLength(1);
  h.runtime.dispose();
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-b" })).rejects.toThrow(/disposed/i);
  expect(window.wuu.listEngines).toHaveBeenCalledTimes(1);
});

it("allows a forced provider catalog refresh and fences its older response", async () => {
  const completions: Array<(value: unknown) => void> = [];
  window.wuu = { loadCodexModels: vi.fn(() => new Promise(resolve => completions.push(resolve))) } as unknown as typeof window.wuu;
  const h = fixture(); h.get().initialized!.providers![0].type = "openai-codex";
  const old = h.runtime.invoke("composer.refresh-provider-models", { ownerTabId: "draft-a", provider: "api" });
  const fresh = h.runtime.invoke("composer.refresh-provider-models", { ownerTabId: "draft-a", provider: "api", force: true });
  expect(window.wuu.loadCodexModels).toHaveBeenCalledTimes(2);
  completions[1]({ provider: "api", models: [{ slug: "new", supported_in_api: true }], providers: [{ name: "api", type: "openai-codex", model: "new" }] });
  await fresh;
  completions[0]({ provider: "api", models: [{ slug: "old", supported_in_api: true }], providers: [{ name: "api", type: "openai-codex", model: "old" }] });
  await old;
  expect(h.view().providerModels.map(model => model.id)).toEqual(["new"]);
  expect(h.get().initialized?.providers?.[0].model).toBe("new");
});

it("ignores provider catalogs that outlive their source tab", async () => {
  let complete!: (value: unknown) => void;
  window.wuu = { loadCodexModels: vi.fn(() => new Promise(resolve => { complete = resolve; })) } as unknown as typeof window.wuu;
  const h = fixture(); h.get().initialized!.providers![0].type = "openai-codex"; const request = h.runtime.invoke("composer.refresh-provider-models", { ownerTabId: "draft-a", provider: "api" });
  h.set({ ...h.get(), activeSessionTabID: "draft-b" });
  complete({ provider: "api", models: [{ slug: "wrong", supported_in_api: true }], providers: [{ name: "api", type: "openai-codex", model: "wrong" }] });
  await request;
  expect(h.view().providerModels).toHaveLength(0);
  expect(h.get().initialized?.providers?.[0].model).toBe("a");
});

it("targets the active split owner rather than the primary thread", async () => {
  window.wuu = { updateRuntimeSettings: vi.fn().mockResolvedValue({ provider: "api", model: "a" }) } as unknown as typeof window.wuu;
  const h = fixture(); const primary = openedThread(h);
  const secondary = { ...primary, id: "secondary", cwd: "/one/tree" };
  h.set({ ...h.get(), secondaryThread: secondary, activePane: "secondary", activeSessionTabID: "thread:secondary", threads: [primary, secondary], sessionTabs: [...h.get().sessionTabs, { id: "thread:secondary", kind: "thread", threadID: secondary.id, context, title: "Secondary", prompt: "", images: [], files: [] }] });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "thread:secondary", selection: { kind: "speed", speed: "fast" } });
  expect(vi.mocked(window.wuu.updateRuntimeSettings).mock.calls[0][6]).toBe("secondary");
  expect(h.get().thread?.speed).toBeUndefined();
  expect(h.get().secondaryThread?.speed).toBe("fast");
});

it("adopts the existing settings inventory without another discovery request", () => {
  installEngineAPI(); let shared = inventory;
  installEngineAPI(); const h = fixture(() => shared);
  expect(h.view().engines.map(engine => engine.id)).toEqual(["wuu", "native"]);
  shared = { engines: [{ id: "replacement", enabled: true, binary_ok: true }] };
  expect(h.view().engines.map(engine => engine.id)).toEqual(["replacement"]);
  expect(window.wuu.listEngines).not.toHaveBeenCalled();
});

it("reports a rejected thread selection without changing its chosen runtime", async () => {
  window.wuu = { updateRuntimeSettings: vi.fn().mockRejectedValue(new Error("Runtime refused the selection")) } as unknown as typeof window.wuu;
  const h = fixture(); openedThread(h);
  await expect(h.runtime.invoke("composer.choose-permission", { ownerTabId: "thread:thread-one", mode: "unconfined" })).resolves.toEqual({ accepted: false });
  expect(h.view().selection.permissionMode).toBe("standard");
  expect(h.view().selectionError).not.toBe("");
});

it("projects submission parameters from the same owner without leaking builtin configuration into native engines", async () => {
  installEngineAPI(); const h = fixture();
  expect(h.runtime.getSubmissionRuntime("missing")).toEqual({ pending: true, requiresHostModel: true, params: {} });
  expect(h.runtime.getSubmissionRuntime("draft-a").pending).toBe(true);
  await h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  expect(h.runtime.getSubmissionRuntime("draft-a").params).toMatchObject({ engine: "wuu", provider: "api", model: "a", effort: "high", permission_mode: "standard" });
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "native" } });
  await h.runtime.invoke("composer.choose-worktree", { ownerTabId: "draft-a", enabled: true, startBranch: "feature" });
  const runtime = h.runtime.getSubmissionRuntime("draft-a");
  expect(runtime).toEqual({ pending: false, requiresHostModel: false, params: { engine: "native", model: "", effort: "", speed: "", permission_mode: "unconfined" }, worktree: { startBranch: "feature" } });
  expect(runtime.params).not.toHaveProperty("provider"); expect(runtime.params).not.toHaveProperty("approve_for_me");
});


it("captures workspace defaults before asynchronous engine discovery", async () => {
  const h = fixture();
  let finish!: (value: EngineListResult) => void;
  window.wuu = { listEngines: vi.fn(() => new Promise<EngineListResult>(resolve => { finish = resolve; })) } as unknown as typeof window.wuu;
  const initialized = { ...configuration(), providers: [{ ...configuration().providers![0], api_key_configured: true }] };
  h.set({ ...h.get(), initialized });
  const pending = h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" });
  h.set({ ...h.get(), activeSessionTabID: "draft-b", initialized: { ...initialized, model: "other" } });
  finish(inventory);
  await expect(pending).resolves.toMatchObject({ context: { kind: "no_project", cwd: "/source" }, params: { engine: "wuu", model: "a", effort: "high" } });
});

it("rejects removed workspace before discovery and preserves native empty overrides", async () => {
  installEngineAPI(); const h = fixture();
  await expect(h.runtime.getWorkspaceSubmissionRuntime(context)).rejects.toThrow();
  expect(window.wuu.listEngines).not.toHaveBeenCalled();
  vi.mocked(window.wuu.listEngines).mockResolvedValue({ ...inventory, settings: { default_engine: "native" } });
  await expect(h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" })).resolves.toEqual({ context: { kind: "no_project", cwd: "/source" }, params: { engine: "native", model: "", effort: "", permission_mode: "unconfined" } });
});


it("marks stale refresh admission as cancellation", async () => {
  installEngineAPI();
  const h = fixture();
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-b" }))
    .rejects.toMatchObject({ name: "AbortError" });
  expect(window.wuu.listEngines).not.toHaveBeenCalled();
});

it("reports current discovery failures but ignores superseded failures", async () => {
  const rejectors: Array<(error: Error) => void> = [];
  window.wuu = { listEngines: vi.fn(() => new Promise((_, reject) => rejectors.push(reject))) } as unknown as typeof window.wuu;
  const h = fixture();
  const old = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  const fresh = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a", force: true });
  rejectors[0](new Error("Old failure"));
  await expect(old).resolves.toBeUndefined();
  const failure = expect(fresh).rejects.toThrow("Engine unavailable");
  rejectors[1](new Error("Engine unavailable"));
  await failure;
  expect(h.view().engineModelsError).toBe("Engine unavailable");
  expect(h.view().engineModelsLoading).toBe(false);
});

it("ignores discovery failures after navigating away", async () => {
  let rejectRequest!: (error: Error) => void;
  window.wuu = { listEngines: vi.fn(() => new Promise((_, reject) => { rejectRequest = reject; })) } as unknown as typeof window.wuu;
  const h = fixture();
  const request = h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  h.set({ ...h.get(), activeSessionTabID: "draft-b" });
  rejectRequest(new Error("Old workspace unavailable"));
  await expect(request).resolves.toBeUndefined();
  expect(h.view().engineModelsError).toBe("");
});


it("distinguishes retired composer ownership from an unproven caller abort", async () => {
  installEngineAPI(); const h = fixture();
  h.set({ ...h.get(), activeSessionTabID: "draft-b" });
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" })).rejects.toBeInstanceOf(PluginLifecycleCancellationError);
  const caller = new AbortController();
  const cause = new DOMException("signal is aborted without reason", "AbortError");
  caller.abort(cause);
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-b" }, caller.signal)).rejects.toBe(cause);
});

// A selected service identity is not its implementation's model dependency.
it.each([false, true, undefined])("uses the selected wuu descriptor's host-model requirement (%s)", async requirement => {
  let shared: EngineListResult = { settings: { default_engine: "wuu" }, engines: [{ id: "wuu", enabled: true, binary_ok: true, requires_host_model: requirement }] };
  installEngineAPI(); const h = fixture(() => shared);
  h.set({ ...h.get(), initialized: { ...configuration(), providers: [] } });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: false, requiresHostModel: requirement !== false });
  if (requirement === false) {
    expect(h.runtime.getSubmissionRuntime("draft-a").params).toMatchObject({ engine: "wuu", model: "", effort: "" });
    expect(h.runtime.getSubmissionRuntime("draft-a").params).not.toHaveProperty("provider");
    window.wuu = { listEngines: vi.fn().mockResolvedValue(shared) } as unknown as typeof window.wuu;
    await expect(h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" })).resolves.toMatchObject({ params: { engine: "wuu", model: "" } });
  } else {
    window.wuu = { listEngines: vi.fn().mockResolvedValue(shared) } as unknown as typeof window.wuu;
    await expect(h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" })).rejects.toThrow(/Configure a model/);
  }
});

it("observes requirement changes without replacing drafts or silently selecting another engine", async () => {
  let shared: EngineListResult = { settings: { default_engine: "wuu" }, engines: [{ id: "wuu", enabled: true, binary_ok: true, requires_host_model: false }] };
  installEngineAPI(); const h = fixture(() => shared);
  h.set({ ...h.get(), sessionTabs: h.get().sessionTabs.map(tab => ({ ...tab, prompt: "keep this input" })) });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: false, requiresHostModel: false });
  shared = { ...shared, engines: [{ ...shared.engines[0], requires_host_model: true }] };
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: false, requiresHostModel: true });
  shared = { settings: { default_engine: "different" }, engines: [{ id: "different", enabled: true, binary_ok: true, requires_host_model: false }] };
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: true, params: { engine: "wuu" } });
  expect(h.view().available).toBe(false);
  expect(h.get().sessionTabs[0]).toMatchObject({ prompt: "keep this input" });
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "wuu" } })).resolves.toEqual({ accepted: false });
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ settings: { default_engine: "wuu" }, engines: [] }) } as unknown as typeof window.wuu;
  await expect(h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" })).rejects.toThrow(/unavailable/);
});

it("honors host-model requirements for an unrelated external engine identity", async () => {
  const shared: EngineListResult = { settings: { default_engine: "external" }, engines: [{ id: "external", enabled: true, binary_ok: true, requires_host_model: true }] };
  installEngineAPI(); const h = fixture(() => shared);
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: false, requiresHostModel: true, params: { engine: "external", provider: "api", model: "a" } });
  window.wuu = { listEngines: vi.fn().mockResolvedValue(shared) } as unknown as typeof window.wuu;
  await expect(h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" })).rejects.toThrow(/Configure a model/);
});

it("fences cached inventory and in-flight catalogs across a provider generation without losing the draft", async () => {
  installEngineAPI(); const h = fixture();
  await h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  expect(h.runtime.getSubmissionRuntime("draft-a").pending).toBe(false);
  h.set({ ...h.get(), initialized: { ...h.get().initialized!, extension_inventory: [] } });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: true, params: { engine: "wuu" } });
  vi.mocked(window.wuu.listEngines).mockResolvedValue({ engines: [{ id: "wuu", enabled: true, binary_ok: true, requires_host_model: false }] });
  await h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: false, requiresHostModel: false });
  expect(window.wuu.listEngines).toHaveBeenCalledTimes(2);
});

it("keeps permission policy independent of a replacement's host-model requirement", async () => {
  installEngineAPI();
  const shared: EngineListResult = { engines: [{ id: "wuu", enabled: true, binary_ok: true, requires_host_model: false }] };
  const h = fixture(() => shared);
  h.set({ ...h.get(), initialized: { ...configuration(), permissions: { mode: "standard", approve_for_me: true } } });
  await h.runtime.invoke("composer.choose-permission", { ownerTabId: "draft-a", mode: "read_only", approveForMe: false });
  expect(h.runtime.getSubmissionRuntime("draft-a").params).toMatchObject({ engine: "wuu", permission_mode: "read_only", approve_for_me: false });
  vi.mocked(window.wuu.listEngines).mockResolvedValue(shared);
  await expect(h.runtime.getWorkspaceSubmissionRuntime({ kind: "no_project", cwd: "/source" })).resolves.toMatchObject({ params: { engine: "wuu", permission_mode: "standard", approve_for_me: true } });
});

it.each([["wuu", false], ["external", true]] as const)("persists model choices from the declared %s model source", async (engine, requiresHostModel) => {
  installEngineAPI(); const shared: EngineListResult = { settings: { default_engine: engine }, engines: [{ id: engine, enabled: true, binary_ok: true, requires_host_model: requiresHostModel, models: [{ id: "own-model" }] }] };
  const h = fixture(() => shared);
  const choice = requiresHostModel ? { kind: "provider-model" as const, provider: "api", model: "b", variant: "low" } : { kind: "native-model" as const, model: "own-model", effort: "" };
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: choice })).resolves.toEqual({ accepted: true });
  expect(h.runtime.getSubmissionRuntime("draft-a").params).toMatchObject({ engine, model: requiresHostModel ? "b" : "own-model" });
});

it.each(["removed", "disabled"])("allows a deliberate target after discovery failure but still refuses a discovered %s engine", async availability => {
  installEngineAPI(); const h = fixture();
  vi.mocked(window.wuu.listEngines).mockRejectedValue(new Error("discovery unavailable"));
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" })).rejects.toThrow("discovery unavailable");
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: true, requiresHostModel: true });
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "wuu" } })).resolves.toEqual({ accepted: true });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: false, requiresHostModel: true, params: { engine: "wuu" } });
  const engines = availability === "removed" ? [] : [{ id: "wuu", enabled: false, binary_ok: false, requires_host_model: false }];
  vi.mocked(window.wuu.listEngines).mockResolvedValue({ settings: { default_engine: "another" }, engines });
  await h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a", force: true });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: true, params: { engine: "wuu" } });
  await expect(h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "wuu" } })).resolves.toEqual({ accepted: false });
});

it("does not carry a discovery-failure selection across provider generations", async () => {
  installEngineAPI(); const h = fixture();
  vi.mocked(window.wuu.listEngines).mockRejectedValue(new Error("discovery unavailable"));
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" })).rejects.toThrow();
  await h.runtime.invoke("composer.choose-runtime", { ownerTabId: "draft-a", selection: { kind: "engine", engine: "wuu" } });
  expect(h.runtime.getSubmissionRuntime("draft-a").pending).toBe(false);
  h.set({ ...h.get(), initialized: { ...h.get().initialized!, extension_inventory: [] } });
  expect(h.runtime.getSubmissionRuntime("draft-a")).toMatchObject({ pending: true, requiresHostModel: true, params: { engine: "wuu" } });
  await expect(h.runtime.invoke("composer.refresh-engine-models", { ownerTabId: "draft-a" })).rejects.toThrow();
  expect(h.runtime.getSubmissionRuntime("draft-a").pending).toBe(true);
});
