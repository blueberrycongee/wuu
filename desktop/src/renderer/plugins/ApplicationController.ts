import type { SetStateAction } from "react";
import type { ApplicationActionMapV1, ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ConversationItemSnapshotV1 } from "../../../../packages/plugin-sdk/src/index";
import type { InitializeResult, PopOutInitResult, ServerEvent, Thread } from "../../shared/protocol";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../../shared/protocol";
import { activeSessionTab, activeThreadForState, activeThreadIDForState, resolveThreadRuntimeContext, sameRuntimeContext, reconcileResumedThreadTurns, handleStreamingNotification, initialState, isThreadRunning, reduceServerEvent,
  serverEventTargetsActiveContext, isCoalescedBackgroundThreadEvent, upsertThread, upsertTurn, updateThreadByID, withLoadedRuntimeSessionTab, bindActiveSessionTabToThread, captureComposerDrafts, persistComposerDrafts, cloneSessionTabDraft, emptyComposerDraft, threadSessionTabID, createDraftSessionTab, ensureSessionTab, sessionTabLabel, type SessionTab, type AppState } from "../AppState";
import { loadPopOutRuntime, loadRuntime, loadRuntimeConfiguration, selectRuntimeContext, loadRuntimeRestore, applyRuntimeRestore, type LoadedRuntimeState } from "../RuntimeLoadState";
import { readComposerDraftRecovery, sameRecoveryContext } from "../ComposerDraftRecovery";
import { readThreadReadState } from "../ThreadReadState";
import { subscribeServerEvents } from "../ServerEvents";
import { streamTextKey, streamTextStore } from "../StreamText";
import { getActiveLocale, translateCurrent } from "../i18n";
import { applyThemePreference, currentAppliedTheme, observeAppliedTheme } from "../Theme";
import { observeAppearance, readAppearance, saveAppearance } from "../AppearancePreferences";
import { applyMessageFlowFontSize } from "../MessageFlowFontSizeSection";
import { ApplicationDraftResources } from "./ApplicationDrafts";
import { toConversationItemSnapshot, toConversationProcessSnapshot } from "./ApplicationSnapshots";

export interface ApplicationShellSelection {
  readonly plugin_id: string;
  readonly generation: string;
  readonly provider_id: string;
}
export type ShellInitializeResult = InitializeResult & { application_shell?: ApplicationShellSelection | null; application_surfaces?: Readonly<Record<string, ApplicationShellSelection>> };

/** Shared domain state survives shell replacement; UI providers own only presentation. */
export class ApplicationController implements ApplicationServiceV1 {
  readonly contractVersion = 1 as const;
  readonly recoveredDrafts = readComposerDraftRecovery();
  private state: AppState;
  private readonly stateListeners = new Set<() => void>();
  private readonly listeners = new Set<() => void>();
  private revision = 0;
  private snapshot: ApplicationSnapshotV1;
  private draft = "";
  private readonly draftResources = new ApplicationDraftResources(() => this.publish());
  private draftRevision = 0;
  private settings = { open: false, pageId: "providers" };
  private preview: ApplicationSnapshotV1["preview"] = null;
  private theme = window.wuu?.initialThemePreference ?? "system";
  private load?: Promise<LoadedRuntimeState>;
  private loaded?: LoadedRuntimeState;
  private pendingEvents: ServerEvent[] = [];
  private eventTimer?: ReturnType<typeof setTimeout>;
  private subscriptions: (() => void)[] = [];
  private consumers = 0;
  private lifetime?: AbortController;
  private navigationRequest = 0;
  private pendingNavigation?: NonNullable<ApplicationSnapshotV1["tabs"]["pending"]>;
  private submitting = false;
  private readonly pendingDraftAdmissions = new Set<string>();
  private streamPublication?: number;

  constructor(state?: AppState) {
    this.state = state ?? { ...initialState, sessionTabs: this.recoveredDrafts.tabs, lastViewedTurnByThreadID: readThreadReadState() };
    const tab = activeSessionTab(this.state);
    this.draft = tab && tab.kind !== "skills" ? tab.prompt : "";
    this.rememberDraftResources();
    this.snapshot = this.project();
  }

  getState = (): AppState => this.state;
  subscribeState = (listener: () => void): (() => void) => { this.stateListeners.add(listener); return () => { this.stateListeners.delete(listener); }; };
  getSnapshot = (): ApplicationSnapshotV1 => this.snapshot;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  updateState = (update: SetStateAction<AppState>): void => {
    this.flushBackgroundEvents();
    // The live composer owns its text immediately. Persist it into its tab
    // before structural changes or navigation, without rerendering the default
    // shell for every keystroke.
    let current = this.state;
    const activeTab = activeSessionTab(current);
    if (activeTab && activeTab.kind !== "skills" && activeTab.prompt !== this.draft) {
      current = { ...current, sessionTabs: current.sessionTabs.map(tab => tab === activeTab ? { ...tab, prompt: this.draft } : tab) };
    }
    let next = typeof update === "function" ? update(current) : update;
    if (next === this.state) return;
    // Navigation can occur with no shell mounted. Clear draft choices at each
    // thread transition, even when navigation returns to the original draft.
    if (activeThreadIDForState(next) !== activeThreadIDForState(current)) {
      next = { ...next, draftWorktree: undefined, draftPermissionMode: "", codexRuntimeMenu: null };
    }
    if (next.activeSessionTabID !== this.state.activeSessionTabID) {
      const tab = activeSessionTab(next);
      this.draft = tab && tab.kind !== "skills" ? tab.prompt : "";
      this.draftRevision++;
    }
    this.state = next;
    this.rememberDraftResources();
    this.publish();
    for (const listener of this.stateListeners) listener();
  };

  start(): () => void {
    if (++this.consumers === 1) {
      this.lifetime = new AbortController();
      this.subscriptions = [subscribeServerEvents(event => this.receive(event)),
        observeAppearance(() => this.publish()), observeAppliedTheme(() => this.publish())];
      const restore = window.wuu?.onRuntimeRestore?.(async () => {
        const signal = this.lifetime!.signal;
        const restored = await loadRuntimeRestore(this.state);
        signal.throwIfAborted();
        this.updateState(state => ({
          ...applyRuntimeRestore(state, restored),
          runtimeConnectionGeneration: (state.runtimeConnectionGeneration ?? 0) + 1,
        }));
      });
      if (restore) this.subscriptions.push(restore);
      void this.initialize().catch(error => this.updateState(state => ({ ...state, status: String(error instanceof Error ? error.message : error) })));
    }
    let stopped = false;
    return () => {
      if (stopped) return;
      stopped = true;
      if (--this.consumers !== 0) return;
      this.lifetime?.abort();
      this.flushBackgroundEvents();
      this.load = undefined;
      this.subscriptions.splice(0).reverse().forEach(stop => stop());
      if (this.streamPublication !== undefined) cancelAnimationFrame(this.streamPublication);
      this.streamPublication = undefined;
    };
  }

  initialize(): Promise<LoadedRuntimeState> {
    if (this.state.initialized) return Promise.resolve({ ...this.state,
      heldComposerMessages: this.loaded?.thread?.id === this.state.thread?.id ? this.loaded?.heldComposerMessages : undefined });
    if (this.load) return this.load;
    const signal = this.lifetime?.signal;
    this.load = (async () => {
      let popOut: PopOutInitResult | undefined;
      if (window.wuu.popOutInit) popOut = window.wuu.popOutInit();
      let loaded: LoadedRuntimeState;
      if (popOut?.kind && popOut.context) loaded = await loadPopOutRuntime(popOut);
      else {
        const listed = await window.wuu.listProjects();
        signal?.throwIfAborted();
        const workspace = listed.active_context ? listed : await window.wuu.selectNoProject(false);
        signal?.throwIfAborted();
        const active = this.recoveredDrafts.tabs.find(tab => tab.id === this.recoveredDrafts.activeTabID
          && workspace.active_context && sameRecoveryContext(tab.context, workspace.active_context));
        loaded = await loadRuntime(workspace, { resumeLatestThread: active?.kind !== "draft", resumeThreadID: active?.kind === "thread" ? active.threadID : undefined });
      }
      signal?.throwIfAborted();
      this.loaded = loaded;
      this.updateState(state => withLoadedRuntimeSessionTab(state, loaded));
      return loaded;
    })();
    return this.load;
  }

  private receive(event: ServerEvent): void {
    if (!serverEventTargetsActiveContext(event, this.state)) return;
    const handling = handleStreamingNotification(event, this.state);
    if (handling === "stream" || handling === "background-stream") {
      // Snapshot readers receive coalesced live text without forcing the default
      // shell's structural state to update for every token.
      if (this.streamPublication === undefined) this.streamPublication = requestAnimationFrame(() => {
        this.streamPublication = undefined;
        this.publish();
      });
      return;
    }
    if (handling === "skip") return;
    if (isCoalescedBackgroundThreadEvent(event, this.state)) {
      this.pendingEvents.push(event);
      this.eventTimer ??= setTimeout(() => this.flushBackgroundEvents(), 150);
      return;
    }
    this.updateState(state => reduceServerEvent(state, event));
  }

  private flushBackgroundEvents(): void {
    if (this.eventTimer !== undefined) clearTimeout(this.eventTimer);
    this.eventTimer = undefined;
    const events = this.pendingEvents;
    this.pendingEvents = [];
    if (events.length > 0) this.updateState(state => events.reduce(reduceServerEvent, state));
  }

  setDraft(text: string): void {
    if (text === this.draft) return;
    this.draft = text;
    this.draftRevision++;
    this.publish({ ...this.snapshot, composer: Object.freeze({ ...this.snapshot.composer, draftText: text }),
      drafts: Object.freeze({ ...this.snapshot.drafts,
        [this.state.secondaryThread ? this.state.activePane : "primary"]: Object.freeze({ ...this.snapshot.drafts[this.state.secondaryThread ? this.state.activePane : "primary"], text }) }),
      tabs: Object.freeze({ ...this.snapshot.tabs, items: Object.freeze(this.snapshot.tabs.items.map(tab =>
        tab.id === this.state.activeSessionTabID ? Object.freeze({ ...tab, draftText: text }) : tab)) }) });
  }

  setSettings(open: boolean, pageId = this.settings.pageId): void {
    if (open === this.settings.open && pageId === this.settings.pageId) return;
    this.settings = { open, pageId };
    this.publish();
  }

  private adoptThread(thread: Thread, select = false): void {
    this.updateState(state => select
      ? withLoadedRuntimeSessionTab({ ...state, threads: upsertThread(state.threads, thread) }, {
        thread, activeContext: state.activeContext ?? resolveThreadRuntimeContext(thread, state.projects),
        secondaryThread: undefined, activePane: "primary", running: isThreadRunning(thread),
      })
      : updateThreadByID(state, thread.id, () => thread));
  }

  private rememberDraftResources(): void {
    for (const tab of this.state.sessionTabs) {
      if (tab.kind === "skills") continue;
      tab.images.forEach(image => this.draftResources.remember("image", image));
      tab.files.forEach(file => this.draftResources.remember("file", file));
    }
  }

  private composerDraftForPane(pane: "primary" | "secondary") {
    const state = this.state;
    const thread = pane === "primary" ? state.thread : state.secondaryThread;
    const tab = state.secondaryThread && thread
      ? state.sessionTabs.find(item => item.id === threadSessionTabID(thread.id)) : pane === "primary" ? activeSessionTab(state) : undefined;
    const draft = tab ? cloneSessionTabDraft(tab) : emptyComposerDraft();
    if (tab?.id === state.activeSessionTabID) draft.prompt = this.draft;
    return draft;
  }

  private async selectTab(tab: SessionTab, signal?: AbortSignal): Promise<void> {
    const request = ++this.navigationRequest;
    const sourceContext = this.state.activeContext;
    const contextSwitching = !sameRuntimeContext(sourceContext, tab.context) || this.pendingNavigation?.contextSwitching === true;
    this.pendingNavigation = Object.freeze({ kind: "tab", targetId: tab.id, contextSwitching });
    this.publish();
    let switchedContext = false;
    try {
      let loaded: Partial<AppState> = {};
      if (contextSwitching) {
        switchedContext = true;
        const workspace = await selectRuntimeContext(tab.context);
        signal?.throwIfAborted();
        if (request !== this.navigationRequest) return;
        loaded = await loadRuntimeConfiguration(workspace);
        signal?.throwIfAborted();
        if (request !== this.navigationRequest) return;
      }
      const resumed = tab.kind === "thread" ? await window.wuu.resumeThread(tab.threadID) : undefined;
      signal?.throwIfAborted();
      if (request !== this.navigationRequest) return;
      if (tab.kind === "thread" && !resumed?.thread) throw new Error("Thread resume returned no thread");
      this.updateState(state => {
        const target = state.sessionTabs.find(item => item.id === tab.id) ?? tab;
        const thread = resumed?.thread && reconcileResumedThreadTurns(resumed.thread,
          [state.thread, state.secondaryThread, ...state.threads].find(item => item?.id === resumed.thread!.id));
        return { ...state, ...loaded, thread, secondaryThread: undefined, activePane: "primary",
          activeContext: tab.context, activeProjectId: tab.context.kind === "project" ? tab.context.project_id : undefined,
          activeSessionTabID: target.id, sessionTabs: ensureSessionTab(state.sessionTabs, target),
          threads: thread ? upsertThread(loaded.threads ?? state.threads, thread) : loaded.threads ?? state.threads,
          allowThreadAutoActivation: target.kind === "thread", running: isThreadRunning(thread), status: "ready" };
      });
    } catch (error) {
      // Restore transport selection as well as the visible source after a failed
      // cross-workspace load. A newer navigation owns transport recovery instead.
      if (switchedContext && sourceContext && request === this.navigationRequest) {
        try { await selectRuntimeContext(sourceContext); }
        catch (restoreError) {
          if (request === this.navigationRequest) this.updateState(state => ({ ...state,
            initialized: undefined, activeContext: undefined, activeProjectId: undefined, status: String(restoreError) }));
        }
      }
      throw error;
    } finally {
      if (request === this.navigationRequest) { this.pendingNavigation = undefined; this.publish(); }
    }
  }

  async invoke<Action extends keyof ApplicationActionMapV1>(action: Action, input: ApplicationActionMapV1[Action]["input"], options?: { readonly signal?: AbortSignal }): Promise<ApplicationActionMapV1[Action]["result"]> {
    const signal = options?.signal;
    signal?.throwIfAborted();
    const result = await this.perform(action, input, signal);
    signal?.throwIfAborted();
    return result as ApplicationActionMapV1[Action]["result"];
  }

  private async perform(action: keyof ApplicationActionMapV1, input: ApplicationActionMapV1[keyof ApplicationActionMapV1]["input"], signal?: AbortSignal): Promise<unknown> {
    switch (action) {
      case "composer.attach": {
        const { files } = input as ApplicationActionMapV1["composer.attach"]["input"];
        return Object.freeze({ attachments: this.draftResources.attach(files) });
      }
      case "composer.resolve-attachment":
      case "composer.retry-attachment": {
        const { attachmentId } = input as ApplicationActionMapV1["composer.resolve-attachment"]["input"];
        return this.draftResources.resolve(attachmentId, action === "composer.retry-attachment");
      }
      case "composer.checkpoint": {
        const checkpoint = input as ApplicationActionMapV1["composer.checkpoint"]["input"];
        if (checkpoint.activeTabId !== this.state.activeSessionTabID) throw new Error("Draft owner changed");
        const primary = this.draftResources.read(checkpoint.primary);
        const secondary = checkpoint.secondary && this.draftResources.read(checkpoint.secondary);
        if (Boolean(this.state.secondaryThread) !== Boolean(secondary)) throw new Error("Composer pane ownership changed");
        const captured = captureComposerDrafts(this.state, secondary ? { primary, secondary } : primary);
        this.draft = captured.activeDraft.prompt;
        this.draftRevision++;
        this.updateState(state => persistComposerDrafts(state, captured));
        return;
      }
      case "tab.select": {
        const { tabId } = input as ApplicationActionMapV1["tab.select"]["input"];
        const tab = this.state.sessionTabs.find(item => item.id === tabId);
        if (!tab) throw new Error(`Unknown tab: ${tabId}`);
        await this.selectTab(tab, signal); return;
      }
      case "tab.close": {
        const { tabId } = input as ApplicationActionMapV1["tab.close"]["input"];
        const index = this.state.sessionTabs.findIndex(item => item.id === tabId);
        if (index < 0) return;
        if (this.state.activeSessionTabID === tabId) {
          const tab = this.state.sessionTabs[index];
          const remaining = this.state.sessionTabs.filter(item => item.id !== tabId);
          const fallback = remaining[Math.min(index, remaining.length - 1)]
            ?? createDraftSessionTab(`draft:${crypto.randomUUID()}`, tab.context);
          await this.selectTab(fallback, signal);
          signal?.throwIfAborted();
          if (this.state.activeSessionTabID !== fallback.id) return;
        }
        this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.filter(item => item.id !== tabId) }));
        return;
      }
      case "tab.reorder": {
        const { tabId, beforeTabId } = input as ApplicationActionMapV1["tab.reorder"]["input"];
        if (tabId === beforeTabId) return;
        const tab = this.state.sessionTabs.find(item => item.id === tabId);
        if (!tab || !this.state.sessionTabs.some(item => item.id === beforeTabId)) throw new Error("Unknown tab reorder destination");
        this.updateState(state => {
          const tabs = state.sessionTabs.filter(item => item.id !== tabId);
          tabs.splice(tabs.findIndex(item => item.id === beforeTabId), 0, state.sessionTabs.find(item => item.id === tabId)!);
          return { ...state, sessionTabs: tabs };
        }); return;
      }
      case "tab.new": {
        const { workspaceId, project } = input as ApplicationActionMapV1["tab.new"]["input"];
        const workspace = workspaceId ? this.state.projects.find(item => item.id === workspaceId) : undefined;
        if (workspaceId && (!workspace || workspace.missing)) throw new Error(`Workspace unavailable: ${workspaceId}`);
        const context = workspace ? { kind: "project" as const, project_id: workspace.id, cwd: workspace.path } : this.state.activeContext;
        if (!context) throw new Error("No workspace is selected");
        if (project && context.kind !== "project") throw new Error("Project drafts require a workspace");
        const existing = this.state.sessionTabs.find(item => item.kind === "draft" && !this.pendingDraftAdmissions.has(item.id) && sameRuntimeContext(item.context, context));
        const tab = existing ?? createDraftSessionTab(`draft:${crypto.randomUUID()}`, context);
        await this.selectTab(tab, signal);
        if (this.state.activeSessionTabID !== tab.id) throw new Error("Draft navigation was superseded");
        if (project !== undefined) await this.invoke("draft.set-project-mode", { project }, { signal });
        return { tabId: tab.id };
      }
      case "draft.rename": {
        const { tabId, title } = input as ApplicationActionMapV1["draft.rename"]["input"];
        if (!title.trim() || !this.state.sessionTabs.some(item => item.id === tabId && item.kind === "draft")) return;
        this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(item => item.id === tabId ? { ...item, title: title.trim() } : item) })); return;
      }
      case "draft.set-project-mode": {
        const { project } = input as ApplicationActionMapV1["draft.set-project-mode"]["input"];
        const tabId = this.state.activeSessionTabID;
        const tab = this.state.sessionTabs.find(item => item.id === tabId);
        if (tab?.kind !== "draft" || Boolean(tab.project) === project) return;
        if (project && tab.context.kind !== "project") throw new Error("Project drafts require a workspace");
        this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(item => item.id === tabId ? { ...item, project: project ? true as const : undefined, title: project ? translateCurrent("projects.newProject")
          : item.title === translateCurrent("projects.newProject") ? translateCurrent("tabs.newConversation") : item.title } : item) })); return;
      }
      case "navigation.show-section": {
        const { section } = input as ApplicationActionMapV1["navigation.show-section"]["input"];
        this.updateState(state => ({ ...state, nativeSection: section })); return;
      }
      case "catalog.select-section": {
        const { section } = input as ApplicationActionMapV1["catalog.select-section"]["input"];
        this.updateState(state => ({ ...state, catalogSection: section })); return;
      }
      case "catalog.select-plugin": {
        const { pluginId } = input as ApplicationActionMapV1["catalog.select-plugin"]["input"];
        this.updateState(state => ({ ...state, catalogPluginID: pluginId })); return;
      }
      case "workspace.select": {
        const request = ++this.navigationRequest;
        const { workspaceId } = input as ApplicationActionMapV1["workspace.select"]["input"];
        const workspace = await window.wuu.selectProject(workspaceId);
        signal?.throwIfAborted();
        const loaded = await loadRuntime(workspace);
        signal?.throwIfAborted();
        if (request === this.navigationRequest) this.updateState(state => withLoadedRuntimeSessionTab(state, loaded));
        return;
      }
      case "thread.select": {
        const request = ++this.navigationRequest;
        const { threadId } = input as ApplicationActionMapV1["thread.select"]["input"];
        const known = this.state.threads.find(thread => thread.id === threadId);
        const target = known ? resolveThreadRuntimeContext(known, this.state.projects) : undefined;
        let configuration: Partial<AppState> | undefined;
        if (target && !sameRuntimeContext(this.state.activeContext, target)) {
          const workspace = await selectRuntimeContext(target);
          signal?.throwIfAborted();
          if (request !== this.navigationRequest) return;
          configuration = await loadRuntimeConfiguration(workspace);
          signal?.throwIfAborted();
        }
        if (request !== this.navigationRequest) return;
        const result = await window.wuu.resumeThread(threadId);
        signal?.throwIfAborted();
        if (!result.thread) throw new Error("Thread resume returned no thread");
        if (request === this.navigationRequest) {
          if (configuration) this.updateState(state => ({ ...state, ...configuration }));
          this.adoptThread(reconcileResumedThreadTurns(result.thread, known), true);
        }
        return;
      }
      case "thread.create": {
        const { workspaceId } = input as ApplicationActionMapV1["thread.create"]["input"];
        if (workspaceId && workspaceId !== this.state.activeProjectId) await this.invoke("workspace.select", { workspaceId }, { signal });
        signal?.throwIfAborted();
        const context = this.state.activeContext;
        const result = await window.wuu.startThread(undefined, context);
        signal?.throwIfAborted();
        this.adoptThread(result.thread, true);
        return { threadId: result.thread.id };
      }
      case "thread.rename": {
        const { threadId, title } = input as ApplicationActionMapV1["thread.rename"]["input"];
        const result = await window.wuu.renameThread(threadId, title);
        signal?.throwIfAborted(); this.adoptThread(result.thread); return;
      }
      case "thread.archive": {
        const { threadId, archived } = input as ApplicationActionMapV1["thread.archive"]["input"];
        const result = await window.wuu.archiveThread(threadId, archived);
        signal?.throwIfAborted(); this.adoptThread(result.thread); return;
      }
      case "composer.set-draft": this.setDraft((input as ApplicationActionMapV1["composer.set-draft"]["input"]).text); return;
      case "composer.submit": {
        if (this.pendingNavigation) throw new Error("Wait for navigation to finish before submitting");
        if (this.submitting) throw new Error("A composer submission is already pending");
        const text = this.draft;
        let draftRevision = this.draftRevision;
        if (!text.trim()) throw new Error("The composer draft is empty");
        const mode = (input as ApplicationActionMapV1["composer.submit"]["input"]).mode ?? "send";
        let thread = activeThreadForState(this.state);
        if (thread?.read_only) throw new Error("The selected thread is read-only");
        const context = thread ? resolveThreadRuntimeContext(thread, this.state.projects) : this.state.activeContext;
        const sourceTabId = this.state.activeSessionTabID;
        if (!thread && sourceTabId) this.pendingDraftAdmissions.add(sourceTabId);
        this.submitting = true; this.publish();
        try {
          if (!thread) {
            const created = await window.wuu.startThread(undefined, context);
            signal?.throwIfAborted();
            thread = created.thread;
            const currentDraft = this.draft;
            const unchanged = this.draftRevision === draftRevision;
            if (this.state.activeSessionTabID === sourceTabId) {
              this.adoptThread(thread, true);
              // Delivery has not been acknowledged, so keep edits made during admission.
              this.setDraft(currentDraft);
              if (unchanged) draftRevision = this.draftRevision;
            } else {
              const createdThread = thread;
              this.updateState(state => ({ ...state, threads: upsertThread(state.threads, createdThread),
                sessionTabs: context ? bindActiveSessionTabToThread(state.sessionTabs, sourceTabId, createdThread, context) : state.sessionTabs }));
            }
          }
          const clientId = crypto.randomUUID();
          signal?.throwIfAborted();
          if (mode === "queue") await window.wuu.queueTurn(thread.id, text, undefined, clientId, undefined, thread.permission_mode, undefined, undefined, context);
          else if (mode === "steer") {
            const turn = thread.turns.findLast(turn => turn.status === "in_progress");
            if (!turn) throw new Error("There is no running turn to steer");
            await window.wuu.steerTurn(thread.id, turn.id, text, undefined, clientId, undefined, undefined, undefined, context);
          } else {
            const result = await window.wuu.startTurn(thread.id, text, undefined, undefined, thread.permission_mode, undefined, undefined, context, clientId);
            signal?.throwIfAborted();
            const threadId = thread.id;
            this.updateState(state => updateThreadByID(state, threadId, current => upsertTurn(current, result.turn)));
          }
          signal?.throwIfAborted();
          if (this.draftRevision === draftRevision) this.setDraft("");
        } finally { this.pendingDraftAdmissions.delete(sourceTabId); this.submitting = false; this.publish(); }
        return;
      }
      case "composer.stop": {
        const thread = activeThreadForState(this.state);
        if (!thread) throw new Error("No thread is selected");
        await window.wuu.interruptTurn(thread.id); return;
      }
      case "composer.configure": {
        const { modelId, runtimeId, permissionId } = input as ApplicationActionMapV1["composer.configure"]["input"];
        const thread = activeThreadForState(this.state);
        if (runtimeId && runtimeId !== (thread?.engine_id ?? "wuu")) throw new Error("Changing an existing thread engine requires a new thread");
        const result = await window.wuu.updateRuntimeSettings(undefined, modelId, undefined, undefined, undefined, permissionId, thread?.id, undefined, this.state.activeContext);
        signal?.throwIfAborted();
        this.updateState(state => ({ ...state, initialized: state.initialized ? { ...state.initialized, ...result } : state.initialized })); return;
      }
      case "settings.open": this.setSettings(true, (input as ApplicationActionMapV1["settings.open"]["input"]).pageId); return;
      case "settings.close": this.setSettings(false); return;
      case "settings.update-value": {
        const { key, value } = input as ApplicationActionMapV1["settings.update-value"]["input"];
        if (key !== "runtime.modelAliases") throw new Error(`Unsupported settings value: ${key}`);
        const result = await window.wuu.updateAdvancedSettings({ model_aliases: { ...value } });
        signal?.throwIfAborted();
        this.updateState(state => ({ ...state, initialized: state.initialized ? { ...state.initialized, ...result } : state.initialized })); return;
      }
      case "appearance.set": {
        const value = input as ApplicationActionMapV1["appearance.set"]["input"];
        if (value.theme !== undefined) { await window.wuu.setThemePreference(value.theme); signal?.throwIfAborted(); this.theme = value.theme; applyThemePreference(value.theme); }
        if (value.uiFontSize !== undefined) { await window.wuu.setMessageFlowFontSize(value.uiFontSize); signal?.throwIfAborted(); applyMessageFlowFontSize(value.uiFontSize); }
        if (value.codeFontSize !== undefined) saveAppearance({ ...readAppearance(), codeSize: value.codeFontSize });
        this.publish(); return;
      }
      case "preview.open": {
        const { resourceId } = input as ApplicationActionMapV1["preview.open"]["input"];
        const file = await window.wuu.readWorkspaceFile(resourceId, this.state.activeContext?.cwd);
        signal?.throwIfAborted();
        this.preview = Object.freeze({ contractVersion: 1, resourceId, workspaceRelativePath: file.path, text: file.text, safeHostUrl: file.renderable_url, binary: file.binary, truncated: file.truncated, sizeBytes: file.size_bytes });
        this.publish(); return;
      }
      case "preview.close": this.preview = null; this.publish(); return;
      case "runtime.refresh": {
        const restored = await loadRuntimeRestore(this.state);
        signal?.throwIfAborted(); this.updateState(state => applyRuntimeRestore(state, restored)); return;
      }
      default: throw new Error(`Unknown application action: ${action}`);
    }
  }

  private publish(snapshot?: ApplicationSnapshotV1): void {
    this.revision++;
    this.snapshot = snapshot ? Object.freeze({ ...snapshot, revision: this.revision }) : this.project();
    for (const listener of this.listeners) listener();
  }

  private project(): ApplicationSnapshotV1 {
    const state = this.state;
    const initialized = state.initialized;
    const thread = activeThreadForState(state);
    const locale = getActiveLocale();
    const running = isThreadRunning(thread);
    const status = initialized ? "connected" : state.status === "connecting" ? "connecting" : "error";
    const primaryDraft = this.composerDraftForPane("primary");
    const secondaryDraft = this.composerDraftForPane("secondary");
    const attachmentIds = new Set([...primaryDraft.images, ...primaryDraft.files, ...secondaryDraft.images, ...secondaryDraft.files].map(item => item.id));
    const turns = (thread?.turns ?? []).map(turn => {
      const items = turn.items.map(item => {
        const key = streamTextKey(turn.id, item.id, "text");
        return streamTextStore.has(key) ? { ...item, text: streamTextStore.get(key) } : item;
      });
      const visible = items.flatMap(item => {
        const kind: ConversationItemSnapshotV1["kind"] = item.type === "user_message" ? "user-message"
          : item.type === "agent_message" ? "assistant-message" : item.type === "reasoning" ? "reasoning"
          : item.type === "tool_call" ? "tool-reference" : "notice";
        return [toConversationItemSnapshot(item, kind)];
      });
      return Object.freeze({ id: turn.id, status: turn.status === "in_progress" ? "running" as const : turn.status === "interrupted" ? "cancelled" as const : turn.status,
        items: Object.freeze(visible), process: toConversationProcessSnapshot(items.filter(item => item.type === "reasoning" || item.type === "tool_call"), turn.status === "in_progress", running, locale) });
    });
    return Object.freeze({ contractVersion: 1, revision: this.revision, phase: initialized ? "ready" : status === "connecting" ? "starting" : "error",
      connection: Object.freeze({ status, message: state.status }),
      workspaces: Object.freeze({ workspaces: Object.freeze(state.projects.map(project => Object.freeze({ id: project.id, name: project.name, root: project.path, available: !project.missing }))), activeWorkspaceId: state.activeProjectId, activeRoot: state.activeContext?.cwd }),
      navigation: Object.freeze({ contractVersion: 1, section: state.nativeSection ?? "conversations", catalogSection: state.catalogSection ?? "plugins", catalogPluginId: state.catalogPluginID ?? "", activeNodeId: thread?.id, nodes: Object.freeze([
        ...state.projects.map(project => Object.freeze({ id: project.id, kind: "project" as const, label: project.name, active: state.activeProjectId === project.id })),
        ...state.threads.filter(thread => !thread.archived && !thread.ephemeral).map(item => Object.freeze({ id: item.id, kind: "thread" as const, label: item.title || item.preview || item.id, active: item.id === thread?.id, pinned: item.pinned, running: isThreadRunning(item) })),
      ]) }),
      tabs: Object.freeze({ activeTabId: state.activeSessionTabID, pending: this.pendingNavigation,
        items: Object.freeze(state.sessionTabs.map(tab => Object.freeze({ id: tab.id, kind: tab.kind,
          title: sessionTabLabel(tab, state), workspaceId: tab.context.kind === "project" ? tab.context.project_id : undefined,
          workspaceRoot: tab.context.cwd, threadId: tab.kind === "thread" ? tab.threadID : undefined,
          project: tab.kind === "draft" && tab.project === true,
          draftText: tab.kind === "skills" ? "" : tab.id === state.activeSessionTabID ? this.draft : tab.prompt,
          hasAttachments: tab.kind !== "skills" && (tab.images.length > 0 || tab.files.length > 0) }))) }),
      conversation: Object.freeze({ threadId: thread?.id, title: thread?.title || thread?.preview, turns: Object.freeze(turns), loading: !initialized }),
      composer: Object.freeze({ contractVersion: 1, draftText: this.draft, threadId: thread?.id, running, readOnly: thread?.read_only, availableSubmissionModes: Object.freeze(running ? ["queue", "steer"] as const : ["send"] as const),
        model: Object.freeze({ id: thread?.model || initialized?.model || "", label: thread?.model || initialized?.model || "", providerId: thread?.model_provider || initialized?.provider }),
        runtime: Object.freeze({ id: thread?.engine_id || "wuu", label: thread?.engine_id || "Wuu" }), permission: Object.freeze({ id: thread?.permission_mode || initialized?.permissions?.mode || "", label: thread?.permission_mode || initialized?.permissions?.mode || "" }), disabledReason: this.submitting ? "Submitting" : undefined }),
      drafts: Object.freeze({ activeTabId: state.activeSessionTabID, activePane: state.activePane, split: Boolean(state.secondaryThread),
        primary: this.draftResources.project(primaryDraft), secondary: this.draftResources.project(secondaryDraft),
        attachments: Object.freeze([...attachmentIds].map(id => this.draftResources.describe(id))) }),
      settings: Object.freeze({ contractVersion: 1, open: this.settings.open, activePageId: this.settings.pageId,
        plugins: Object.freeze((initialized?.extension_inventory ?? []).filter(item => item.kind === "plugin").map(item => Object.freeze({ id: item.id, name: item.name, enabled: item.enabled !== false, status: item.runtime_state ?? item.state }))),
        providers: Object.freeze((initialized?.providers ?? []).map(provider => Object.freeze({ id: provider.name, label: provider.name, configured: provider.api_key_configured === true }))) }),
      preview: this.preview,
      appearance: Object.freeze({ theme: this.theme, resolvedTheme: currentAppliedTheme(), uiFontSize: Number.parseFloat(document.documentElement.style.getPropertyValue("--conversation-message-font-size")) || MESSAGE_FLOW_FONT_SIZE_RANGE.default, codeFontSize: readAppearance().codeSize }),
      locale, status: Object.freeze({ contractVersion: 1, items: Object.freeze([Object.freeze({ id: "connection", label: state.status, kind: status === "error" ? "error" as const : "info" as const })]) }),
    });
  }
}

let desktopApplication: ApplicationController | undefined;
export function getDesktopApplication(): ApplicationController {
  return desktopApplication ??= new ApplicationController();
}
