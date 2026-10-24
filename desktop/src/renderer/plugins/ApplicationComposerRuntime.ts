import type { ApplicationSettingsViewV1 } from "../../../../packages/plugin-sdk/src/application-settings";
import type { SetStateAction } from "react";
import type { ApplicationComposerRuntimeActionMapV1 as Actions, ApplicationComposerRuntimeChoiceV1, ApplicationComposerRuntimeOwnerV1, ApplicationComposerRuntimeSelectionV1, ApplicationComposerRuntimeViewV1 } from "../../../../packages/plugin-sdk/src/ui-composer-runtime";
import type { EngineListResult, InitializeResult, RuntimeContext, Thread, ThreadStartParams } from "../../shared/protocol";
import { isThreadRunning, sameRuntimeContext, type AppState } from "../AppState";
import type { CodexModelLoadState } from "../ComposerTypes";
import { createRuntimeSettingsActions } from "../RuntimeSettingsActions";
import { rememberedEngineRuntime, resolveDraftEngineMemory, writeDraftEngineMemory } from "../DraftEngineMemory";
import { isProjectCoordinator } from "../ProjectSessions";
import { hasAuthenticatedProvider, providerIsCodex } from "../RuntimeHelpers";
import { runtimeViewForSession } from "../SessionRuntimeState";

const actionNames = new Set<keyof Actions>(["composer.choose-runtime", "composer.choose-permission", "composer.choose-worktree", "composer.refresh-engine-models", "composer.refresh-provider-models"]);
export function isApplicationComposerRuntimeAction(action: string): action is keyof Actions { return actionNames.has(action as keyof Actions); }
const engineFreshnessMS = 120_000;
const emptyEngines: EngineListResult["engines"] = Object.freeze([]) as unknown as EngineListResult["engines"];
const emptyProviders: NonNullable<InitializeResult["providers"]> = Object.freeze([]) as unknown as NonNullable<InitializeResult["providers"]>;

function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => {
    if (!item || typeof item !== "object" || Object.isFrozen(item)) return;
    Object.values(item).forEach(freeze); Object.freeze(item);
  };
  freeze(copy); return copy;
}
type NativeRuntime = { model: string; effort: string; speed?: string; model_options?: Record<string, string> };
type Owner = {
  id: string; key: string; context: RuntimeContext; cwd: string; configuration?: InitializeResult;
  builtin?: ApplicationComposerRuntimeSelectionV1; engine: string; seeded: boolean;
  native: Map<string, NativeRuntime>; permission?: string; approveForMe?: boolean;
  worktree?: { startBranch: string }; git?: AppState["gitStatus"];
  inventory?: EngineListResult; sharedInventory?: ApplicationSettingsViewV1["engineInventory"]; fetchedAt: number; engineRequest: number; enginePending?: Promise<void>; engineSignal?: AbortSignal; engineLoading: boolean; engineError: string;
  codex: CodexModelLoadState; providerRequest: number; providerPending?: Promise<void>; providerSignal?: AbortSignal; providerModelsSource?: CodexModelLoadState["models"]; providerModelsView?: ApplicationComposerRuntimeOwnerV1["providerModels"];
  variants: Map<string, string>; selectionError: string; mutations: number; snapshot?: ApplicationComposerRuntimeOwnerV1; snapshotKey?: string;
};
type Tab = AppState["sessionTabs"][number];
type Source = { owner: Owner; tab: Tab; thread?: Thread; generation: AppState["runtimeConnectionGeneration"]; signal?: AbortSignal };

/** Host-owned runtime selections survive composer replacement; UI owns only its menus. */
export class ApplicationComposerRuntime {
  private readonly owners = new Map<string, Owner>();
  private readonly providerViews = new WeakMap<object, NonNullable<InitializeResult["providers"]>>();
  private readonly pendingRuntimeSelections = new Map<string, Promise<void>>();
  private snapshot?: ApplicationComposerRuntimeViewV1;
  private disposed = false;
  private initialOwner?: string;
  constructor(private readonly host: {
    getState(): AppState;
    updateState(update: SetStateAction<AppState>): void;
    changed(): void;
    isNavigationPending(): boolean;
    isOwnerPending?(ownerTabId: string): boolean;
    getEngineInventory?(): ApplicationSettingsViewV1["engineInventory"];
  }) {}

  private thread(state: AppState, tab: Tab): Thread | undefined {
    return tab.kind === "thread" ? [state.thread, state.secondaryThread, ...state.threads].find(thread => thread?.id === tab.threadID) : undefined;
  }
  private sameContext(state: AppState, context: RuntimeContext): boolean {
    return sameRuntimeContext(state.activeContext, context) && state.activeContext?.cwd === context.cwd;
  }
  private sync(): AppState {
    const state = this.host.getState();
    this.initialOwner ??= state.activeSessionTabID || undefined;
    const present = new Set<string>();
    const sharedInventory = this.host.getEngineInventory?.();
    for (const tab of state.sessionTabs) {
      if (tab.kind !== "draft" && tab.kind !== "thread") continue;
      present.add(tab.id);
      const thread = this.thread(state, tab);
      const cwd = thread?.cwd || tab.context.cwd;
      const key = JSON.stringify([tab.context, cwd]);
      let owner = this.owners.get(tab.id);
      if (!owner || owner.key !== key) {
        owner = { id: tab.id, key, context: { ...tab.context }, cwd, engine: "", seeded: false, native: new Map(), fetchedAt: 0,
          engineRequest: 0, engineLoading: false, engineError: "", codex: { loading: false, error: "", models: [] }, providerRequest: 0, variants: new Map(), selectionError: "", mutations: 0 };
        if (tab.id === this.initialOwner && tab.kind === "draft") {
          owner.permission = state.draftPermissionMode || undefined;
          if (state.draftWorktree?.cwd === tab.context.cwd) owner.worktree = { startBranch: state.draftWorktree.startBranch };
        }
        this.owners.set(tab.id, owner);
      }
      if (this.sameContext(state, tab.context)) {
        owner.configuration = state.initialized;
        owner.git = state.gitStatus;
        if (!owner.builtin && state.initialized) owner.builtin = this.builtinSelection(state.initialized);
        if (tab.id === state.activeSessionTabID && sharedInventory && sharedInventory !== owner.sharedInventory) {
          owner.sharedInventory = sharedInventory;
          owner.inventory = immutable(sharedInventory) as EngineListResult;
          owner.engineRequest++; owner.engineLoading = false; owner.enginePending = undefined;
          owner.fetchedAt = Date.now(); owner.engineError = ""; this.seed(owner, tab);
        }
      }
    }
    for (const id of this.owners.keys()) if (!present.has(id)) this.owners.delete(id);
    return state;
  }
  private builtinSelection(configuration: InitializeResult): ApplicationComposerRuntimeSelectionV1 {
    return { engine: "wuu", provider: configuration.provider, model: configuration.model, effort: configuration.effort ?? "", variant: configuration.variant ?? configuration.effort ?? "",
      speed: configuration.speed ?? "", modelOptions: {}, permissionMode: configuration.permissions?.mode ?? "standard", approveForMe: configuration.permissions?.approve_for_me ?? false };
  }
  private selection(owner: Owner, tab: Tab, thread?: Thread): ApplicationComposerRuntimeSelectionV1 {
    const project = tab.kind === "draft" && tab.project || thread && isProjectCoordinator(thread);
    const engine = project ? "wuu" : thread ? thread.engine_id || "wuu" : owner.engine || owner.inventory?.settings?.default_engine || "wuu";
    const builtin = thread && owner.configuration ? this.builtinSelection(runtimeViewForSession(owner.configuration, thread)!) : owner.builtin;
    const native = thread ? { model: thread.model, effort: thread.model_effort ?? thread.model_variant ?? "", speed: thread.speed, model_options: thread.model_options } : owner.native.get(engine);
    return { engine, provider: builtin?.provider ?? "", model: engine === "wuu" ? builtin?.model ?? "" : native?.model ?? "",
      effort: engine === "wuu" ? builtin?.effort ?? "" : native?.effort ?? "", variant: engine === "wuu" ? builtin?.variant ?? "" : "",
      speed: engine === "wuu" ? builtin?.speed ?? "" : native?.speed ?? "", modelOptions: engine === "wuu" ? {} : native?.model_options ?? {},
      permissionMode: project && !thread ? "read_only" : thread?.permission_mode || owner.permission || (engine === "wuu" ? builtin?.permissionMode ?? "standard" : "unconfined"),
      approveForMe: thread?.approve_for_me ?? owner.approveForMe ?? builtin?.approveForMe ?? false };
  }
  getSnapshot(): ApplicationComposerRuntimeViewV1 {
    if (this.disposed) return this.snapshot!;
    const state = this.sync();
    const rows: ApplicationComposerRuntimeOwnerV1[] = [];
    for (const tab of state.sessionTabs) {
      const owner = this.owners.get(tab.id); if (!owner) continue;
      const thread = this.thread(state, tab); const selection = this.selection(owner, tab, thread);
      const models = owner.inventory?.engines.find(engine => engine.id === selection.engine)?.models;
      const defaultModel = models?.find(model => model.is_default);
      const model = selection.model ? models?.find(model => model.id === selection.model) : defaultModel;
      const nativeDefaults = { model: defaultModel?.id ?? "", effort: model?.default_effort ?? "", speed: model?.default_speed ?? "", modelOptions: Object.fromEntries((model?.options ?? []).map(option => [option.id, option.default_value])) };
      const project = Boolean(tab.kind === "draft" && tab.project || thread && isProjectCoordinator(thread));
      const worktree = { available: tab.kind === "draft" && !project && Boolean(owner.git?.is_repo), enabled: Boolean(owner.worktree && owner.git?.is_repo), startBranch: owner.worktree?.startBranch ?? "", branches: owner.git?.branches ?? [] };
      const sourceProviders = owner.configuration?.providers ?? emptyProviders;
      let providers = this.providerViews.get(sourceProviders);
      if (!providers) { providers = immutable(sourceProviders); this.providerViews.set(sourceProviders, providers); }
      const engines = owner.inventory?.engines ?? emptyEngines;
      if (owner.providerModelsSource !== owner.codex.models) {
        owner.providerModelsSource = owner.codex.models;
        owner.providerModelsView = immutable(owner.codex.models.map(model => ({ id: model.slug, label: model.display_name || model.slug, efforts: model.supported_reasoning ?? [], defaultEffort: model.default_reasoning_level ?? "", supportedInAPI: model.supported_in_api })));
      }
      const providerModels = owner.providerModelsView!;
      const engineSelectionPending = tab.kind === "draft" && !project && !owner.engine && !owner.inventory && typeof window.wuu.listEngines === "function";
      const available = Boolean(owner.configuration && (tab.kind === "draft" || thread));
      const busy = Boolean(owner.mutations || thread && isThreadRunning(thread) || this.host.isOwnerPending?.(tab.id) || this.host.isNavigationPending());
      const key = JSON.stringify([selection, nativeDefaults, worktree, available, engineSelectionPending, busy, thread?.read_only, project, owner.engineLoading, owner.engineError, owner.codex.loading, owner.codex.error, owner.selectionError]);
      if (owner.snapshotKey !== key || owner.snapshot?.providers !== providers || owner.snapshot?.engines !== engines || owner.snapshot?.providerModels !== providerModels) {
        owner.snapshotKey = key;
        owner.snapshot = Object.freeze({ ownerTabId: tab.id, threadId: thread?.id, cwd: owner.cwd, available, readOnly: Boolean(thread?.read_only), busy,
          engineLocked: tab.kind === "thread" || project, engineSelectionPending, permissionLocked: project,
          selection: immutable(selection), nativeDefaults: immutable(nativeDefaults), worktree: immutable(worktree), engines, providers, providerModels,
          engineModelsLoading: owner.engineLoading, engineModelsError: owner.engineError, providerModelsLoading: owner.codex.loading, providerModelsError: owner.codex.error, selectionError: owner.selectionError });
      }
      rows.push(owner.snapshot!);
    }
    const activeOwnerTabId = this.owners.has(state.activeSessionTabID) ? state.activeSessionTabID : undefined;
    if (!this.snapshot || this.snapshot.activeOwnerTabId !== activeOwnerTabId || this.snapshot.owners.length !== rows.length || rows.some((row, index) => this.snapshot!.owners[index] !== row)) {
      this.snapshot = Object.freeze({ activeOwnerTabId, owners: Object.freeze(rows) });
    }
    return this.snapshot;
  }
  /** Host submission uses the same selection policy as the public composer view. */
  getSubmissionRuntime(ownerTabId: string): { pending: boolean; params: ThreadStartParams; worktree?: { startBranch: string } } {
    const owner = this.getSnapshot().owners.find(owner => owner.ownerTabId === ownerTabId);
    if (!owner) return { pending: true, params: {} };
    const selection = owner.selection;
    const params: ThreadStartParams = selection.engine === "wuu"
      ? { engine: selection.engine, provider: selection.provider, model: selection.model,
          effort: selection.variant || selection.effort, speed: selection.speed,
          permission_mode: selection.permissionMode, approve_for_me: selection.approveForMe }
      : { engine: selection.engine, model: selection.model, effort: selection.effort, speed: selection.speed,
          permission_mode: selection.permissionMode,
          ...(Object.keys(selection.modelOptions).length ? { model_options: { ...selection.modelOptions } } : {}) };
    return { pending: !owner.available || owner.engineSelectionPending, params,
      ...(owner.worktree.enabled ? { worktree: { startBranch: owner.worktree.startBranch } } : {}) };
  }
  /** Background workspace entry uses captured workspace defaults, never a draft selection. */
  async getWorkspaceSubmissionRuntime(workspace: RuntimeContext): Promise<{ context: RuntimeContext; params: ThreadStartParams }> {
    if (this.disposed) throw new Error("Composer runtime is disposed");
    const state = this.host.getState();
    if (!state.initialized || this.host.isNavigationPending()) throw new Error("Workspace runtime is not ready");
    const project = workspace.kind === "project" ? state.projects.find(project => project.id === workspace.project_id) : undefined;
    if (workspace.kind === "project" && (!project || project.missing)) throw new Error("Workspace is no longer available");
    const context: RuntimeContext = project ? { kind: "project", project_id: project.id, cwd: project.path } : { ...workspace };
    const configuration = state.initialized;
    const inventory = await window.wuu.listEngines({ cwd: context.cwd }, context);
    if (this.disposed) throw new Error("Composer runtime is disposed");
    const engine = inventory.settings?.default_engine || "wuu";
    if (engine === "wuu" && !hasAuthenticatedProvider(configuration.providers)) throw new Error("Configure a model before sending");
    const params: ThreadStartParams = engine === "wuu"
      ? { engine, provider: configuration.provider, model: configuration.model, effort: configuration.variant || configuration.effort,
          speed: configuration.speed, permission_mode: configuration.permissions?.mode, approve_for_me: configuration.permissions?.approve_for_me }
      : { engine, model: "", effort: "", permission_mode: "unconfined" };
    return { context, params };
  }
  private admit(ownerTabId: string, signal?: AbortSignal): Source {
    if (this.disposed) throw new Error("Composer runtime is disposed");
    signal?.throwIfAborted(); const state = this.sync();
    const owner = this.owners.get(ownerTabId); const tab = state.sessionTabs.find(tab => tab.id === ownerTabId);
    if (!owner || !tab || state.activeSessionTabID !== ownerTabId || !this.sameContext(state, tab.context) || this.host.isNavigationPending()) throw new Error("Composer runtime owner is no longer active");
    return { owner, tab, thread: this.thread(state, tab), generation: state.runtimeConnectionGeneration, signal };
  }
  private current(source: Source): boolean {
    const state = this.host.getState();
    return !this.disposed && !source.signal?.aborted && this.owners.get(source.owner.id) === source.owner
      && state.sessionTabs.some(tab => tab.id === source.owner.id && JSON.stringify([tab.context, this.thread(state, tab)?.cwd || tab.context.cwd]) === source.owner.key)
      && state.activeSessionTabID === source.owner.id && this.sameContext(state, source.owner.context)
      && state.runtimeConnectionGeneration === source.generation && !this.host.isNavigationPending();
  }
  private runtime(source: Source, onWrite: () => void = () => {}, catalog?: { request: number; force?: boolean }) {
    const owner = source.owner;
    const projectedState = (): AppState => {
      const state = this.host.getState();
      if (source.tab.kind !== "draft" || !this.current(source) || !state.initialized || !owner.builtin) return state;
      const selection = owner.builtin;
      return { ...state, initialized: { ...state.initialized, provider: selection.provider, model: selection.model, effort: selection.effort, variant: selection.variant, speed: selection.speed,
        permissions: { ...state.initialized.permissions, mode: owner.permission ?? selection.permissionMode, approve_for_me: owner.approveForMe ?? selection.approveForMe } } };
    };
    return createRuntimeSettingsActions({ getAppState: projectedState, setAppState: update => {
      if (this.disposed || source.signal?.aborted || catalog && (!this.current(source) || catalog.request !== owner.providerRequest)) return;
      if (source.tab.kind === "draft") {
        if (!this.current(source)) return;
        const before = projectedState(); const next = typeof update === "function" ? update(before) : update;
        if (next.initialized) {
          owner.builtin = this.builtinSelection(next.initialized);
          owner.permission = owner.builtin.permissionMode; owner.approveForMe = owner.builtin.approveForMe;
          if (next.initialized.providers !== before.initialized?.providers) this.host.updateState(state => ({ ...state, initialized: state.initialized && { ...state.initialized, providers: next.initialized!.providers } }));
        }
      } else this.host.updateState(update);
      onWrite(); this.host.changed();
    }, getViewContextSwitchPending: () => this.host.isNavigationPending(), getCodexModels: () => catalog?.force ? { ...owner.codex, loading: false } : owner.codex,
      setCodexModels: update => { if (this.current(source) && (!catalog || catalog.request === owner.providerRequest)) { owner.codex = typeof update === "function" ? update(owner.codex) : update; this.host.changed(); } },
      setRuntimeMenuOpen: () => {}, setAccessMenuOpen: () => {}, setBranchMenuOpen: () => {}, setCodexRuntimeMenu: () => {}, clearThreadPendingComposerMessages: () => {},
      variantByModel: owner.variants, pendingRuntimeSelections: this.pendingRuntimeSelections });
  }
  async invoke<K extends keyof Actions>(action: K, input: Actions[K]["input"], signal?: AbortSignal): Promise<Actions[K]["result"]> {
    const source = this.admit(input.ownerTabId, signal);
    if (action === "composer.refresh-engine-models") { await this.refreshEngines(source, (input as Actions["composer.refresh-engine-models"]["input"]).force); return undefined as Actions[K]["result"]; }
    if (action === "composer.refresh-provider-models") { await this.refreshProvider(source, input as Actions["composer.refresh-provider-models"]["input"]); return undefined as Actions[K]["result"]; }
    const owner = source.owner;
    const locked = !owner.configuration || source.tab.kind === "thread" && !source.thread || source.thread?.read_only || source.thread && isThreadRunning(source.thread) || this.host.isOwnerPending?.(owner.id);
    if (locked) return { accepted: false } as Actions[K]["result"];
    owner.mutations++; owner.selectionError = ""; this.host.changed();
    let accepted = false;
    try {
      if (action === "composer.choose-runtime") accepted = await this.choose(source, (input as Actions["composer.choose-runtime"]["input"]).selection);
      else if (action === "composer.choose-permission") {
        const value = input as Actions["composer.choose-permission"]["input"];
        const before = this.selection(owner, source.tab, source.thread);
        if (source.tab.kind === "draft" && source.tab.project || source.thread && isProjectCoordinator(source.thread)) accepted = false;
        else if (!source.thread && before.engine !== "wuu") { owner.permission = value.mode; if (value.approveForMe !== undefined) owner.approveForMe = value.approveForMe; accepted = true; }
        else {
          accepted = before.permissionMode === value.mode && (value.approveForMe === undefined || before.approveForMe === value.approveForMe);
          await this.runtime(source, () => { accepted = true; }).selectPermissionMode(value.mode, value.approveForMe);
        }
      } else if (action === "composer.choose-worktree") {
        const value = input as Actions["composer.choose-worktree"]["input"];
        if (source.tab.kind === "draft" && !source.tab.project && owner.git?.is_repo) {
          if (value.startBranch && !owner.git.branches?.includes(value.startBranch)) throw new Error("Unknown worktree start branch");
          owner.worktree = value.enabled ? { startBranch: value.startBranch ?? "" } : undefined; accepted = true;
        }
      } else throw new Error(`Unknown composer runtime action: ${action}`);
      signal?.throwIfAborted();
      if (!accepted) owner.selectionError = "Runtime selection was not accepted";
      return { accepted } as Actions[K]["result"];
    } catch (error) { if (this.current(source)) owner.selectionError = error instanceof Error ? error.message : String(error); throw error; }
    finally { owner.mutations--; if (!this.disposed) this.host.changed(); }
  }
  private async choose(source: Source, choice: ApplicationComposerRuntimeChoiceV1): Promise<boolean> {
    const owner = source.owner; const selected = this.selection(owner, source.tab, source.thread);
    if (choice.kind === "engine") {
      if (source.tab.kind !== "draft" || source.tab.project) return false;
      if (choice.engine !== "wuu" && !owner.inventory?.engines.some(engine => engine.id === choice.engine && engine.enabled && engine.binary_ok)) return false;
      owner.engine = choice.engine; owner.seeded = true; owner.permission = choice.engine === "wuu" ? owner.builtin?.permissionMode : "unconfined";
      const native = owner.native.get(choice.engine) ?? rememberedEngineRuntime(choice.engine, owner.inventory) ?? { model: "", effort: "" };
      owner.native.set(choice.engine, native); writeDraftEngineMemory({ engine: choice.engine, ...native }); return true;
    }
    const runtime = this.runtime(source);
    if (choice.kind === "provider-model") return selected.engine === "wuu" && runtime.selectRuntimeModel(choice.provider, choice.model, choice.variant);
    if (choice.kind === "effort") return selected.engine === "wuu" && runtime.selectRuntimeEffort(choice.variant);
    if (choice.kind === "speed" && (source.thread || selected.engine === "wuu")) return runtime.selectRuntimeSpeed(choice.speed);
    if (selected.engine === "wuu") return false;
    const update = choice.kind === "native-model" ? { reset_model: true, model: choice.model, effort: choice.effort }
      : choice.kind === "native-effort" ? { effort: choice.effort }
      : choice.kind === "native-options" ? { model_options: { ...choice.modelOptions } }
      : { speed: choice.speed };
    if (source.thread) return runtime.selectEngineRuntime(update);
    const previous = owner.native.get(selected.engine) ?? { model: "", effort: "" };
    const next = choice.kind === "native-model" ? { model: choice.model, effort: choice.effort } : { ...previous, ...update };
    owner.native.set(selected.engine, next); writeDraftEngineMemory({ engine: selected.engine, ...next }); return true;
  }
  private async refreshEngines(source: Source, force = false): Promise<void> {
    const owner = source.owner;
    if (!force && owner.inventory && Date.now() - owner.fetchedAt < engineFreshnessMS) return;
    if (!force && owner.enginePending && !owner.engineSignal?.aborted) return owner.enginePending.then(() => { source.signal?.throwIfAborted(); });
    const request = ++owner.engineRequest; owner.engineSignal = source.signal; owner.engineLoading = true; owner.engineError = ""; this.host.changed();
    const pending = (async () => {
      try {
        const result = await window.wuu.listEngines({ cwd: owner.cwd, ...(force ? { refresh_models: true } : {}) }, owner.context);
        source.signal?.throwIfAborted();
        if (!this.current(source) || request !== owner.engineRequest) return;
        owner.inventory = immutable(result); owner.fetchedAt = Date.now();
        this.seed(owner, source.tab);
      } catch (error) {
        if (this.current(source) && request === owner.engineRequest) owner.engineError = error instanceof Error ? error.message : String(error);
        if (source.signal?.aborted) throw error;
      } finally {
        if (request === owner.engineRequest) { owner.engineLoading = false; owner.enginePending = undefined; if (!this.disposed) this.host.changed(); }
      }
    })();
    owner.enginePending = pending; return pending;
  }
  private seed(owner: Owner, tab: Tab): void {
    if (owner.seeded || tab.kind !== "draft" || tab.project || !owner.inventory) return;
    const remembered = resolveDraftEngineMemory(owner.inventory);
    if (remembered) { owner.engine = remembered.engine; owner.native.set(remembered.engine, remembered); }
    owner.seeded = true;
  }
  private async refreshProvider(source: Source, input: Actions["composer.refresh-provider-models"]["input"]): Promise<void> {
    const owner = source.owner; const provider = input.provider ?? this.selection(owner, source.tab, source.thread).provider;
    if (!owner.configuration || !providerIsCodex(owner.configuration, provider)) return;
    if (!input.force && owner.providerPending && !owner.providerSignal?.aborted && owner.codex.provider === provider) return owner.providerPending.then(() => { source.signal?.throwIfAborted(); });
    const request = ++owner.providerRequest; owner.providerSignal = source.signal;
    const pending = this.runtime(source, undefined, { request, force: input.force }).loadCodexModelsForProvider(provider, input.force).then(() => { source.signal?.throwIfAborted(); }).finally(() => {
      if (request === owner.providerRequest) { owner.providerPending = undefined; if (owner.codex.loading) owner.codex = { ...owner.codex, loading: false }; if (!this.disposed) this.host.changed(); }
    });
    owner.providerPending = pending; return pending;
  }
  dispose(): void { this.disposed = true; this.owners.clear(); this.snapshot = Object.freeze({ owners: Object.freeze([]) }); }
}
