import type { ApplicationNavigationActionMapV1 } from "../../../../packages/plugin-sdk/src/ui-navigation";
import type { ApplicationPreviewActionMapV1 } from "../../../../packages/plugin-sdk/src/ui-preview";
import type { ApplicationSettingsActionMapV1 } from "../../../../packages/plugin-sdk/src/application-settings";
import type { SetStateAction } from "react";
import type { ApplicationActionMapV1, ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ConversationItemSnapshotV1 } from "../../../../packages/plugin-sdk/src/index";
import type { InitializeResult, PopOutInitResult, ServerEvent, Thread } from "../../shared/protocol";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../../shared/protocol";
import { activeSessionTab, summarizeThreadsForSidebar, threadForTab, activeThreadForState, activeThreadIDForState, resolveThreadRuntimeContext, sameRuntimeContext, reconcileResumedThreadTurns, handleStreamingNotification, initialState, isThreadRunning, reduceServerEvent,
  serverEventTargetsActiveContext, isCoalescedBackgroundThreadEvent, upsertThread, upsertTurn, updateThreadByID, withLoadedRuntimeSessionTab, bindActiveSessionTabToThread, captureComposerDrafts, persistComposerDrafts, cloneSessionTabDraft, emptyComposerDraft, threadSessionTabID, createDraftSessionTab, ensureSessionTab, sessionTabLabel, type ComposerDraftState, type SessionTab, type AppState } from "../AppState";
import { loadPopOutRuntime, loadRuntime, loadRuntimeConfiguration, selectRuntimeContext, loadRuntimeRestore, applyRuntimeRestore, type LoadedRuntimeState } from "../RuntimeLoadState";
import { readComposerDraftRecovery, sameRecoveryContext } from "../ComposerDraftRecovery";
import { readThreadReadState } from "../ThreadReadState";
import { subscribeServerEvents } from "../ServerEvents";
import { streamTextKey, streamTextStore } from "../StreamText";
import { getActiveLocale, translateCurrent } from "../i18n";
import { applyThemePreference, currentAppliedTheme, observeAppliedTheme } from "../Theme";
import { observeAppearance, readAppearance, saveAppearance } from "../AppearancePreferences";
import { applyMessageFlowFontSize } from "../MessageFlowFontSizeSection";
import { awaitComposerFiles, awaitComposerImages, createComposerMessage, createOptimisticTurn, queuedMessagePreview, queuedMessageFullPreview, type QueuedComposerMessage, inputFilesFromComposer, inputImagesFromComposer } from "../ComposerMessages";
import { createComposerPendingState } from "../ComposerPendingState";
import { createSidebarThreadCache } from "../SidebarWorkspaceState";
import { createConversationPaneActions } from "../ConversationPaneActions";
import { createThreadMutationActions } from "../ThreadMutationActions";
import { createSessionTabActions } from "../SessionTabActions";
import { createThreadActivationActions } from "../ThreadActivationActions";
import { createWorkspaceRuntimeActions } from "../WorkspaceRuntimeActions";
import { createApplicationSettingsControls } from "./hostApplicationSettingsControls";
import { ApplicationNavigation, isApplicationNavigationAction } from "./ApplicationNavigation";
import { ApplicationPreview, isApplicationPreviewAction } from "./ApplicationPreview";
import { ApplicationSettings, isApplicationSettingsAction } from "./ApplicationSettings";
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
  readonly settingsControls = createApplicationSettingsControls();
  readonly recoveredDrafts = readComposerDraftRecovery();
  private state: AppState;
  private readonly stateListeners = new Set<() => void>();
  private readonly listeners = new Set<() => void>();
  private revision = 0;
  private snapshot: ApplicationSnapshotV1;
  private draft = "";
  private readonly draftResources = new ApplicationDraftResources(() => this.publish());
  private draftRevision = 0;
  private readonly applicationSettings: ApplicationSettings;
  private readonly sidebarCache: ReturnType<typeof createSidebarThreadCache>;
  private readonly pendingComposer: ReturnType<typeof createComposerPendingState>;
  private readonly applicationPreview: ApplicationPreview;
  private readonly applicationNavigation: ApplicationNavigation;
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
  private navigationAbortCleanup?: () => void;
  private abortedNavigation?: { request: number; signal: AbortSignal; context: NonNullable<AppState["activeContext"]> };
  private pendingNavigation?: NonNullable<ApplicationSnapshotV1["tabs"]["pending"]>;
  private readonly submitting = new Set<string>();
  private readonly pendingDraftAdmissions = new Set<string>();
  private streamPublication?: number;

  constructor(state?: AppState) {
    this.state = state ?? { ...initialState, sessionTabs: this.recoveredDrafts.tabs, lastViewedTurnByThreadID: readThreadReadState() };
    this.sidebarCache = createSidebarThreadCache(() => this.state.projects, status => { throw new Error(status); });
    this.pendingComposer = createComposerPendingState({ getAppState: () => this.state,
      getComposerDraftForThread: id => {
        const tab = this.state.sessionTabs.find(tab => tab.id === id || tab.kind === "thread" && tab.threadID === id);
        const draft = tab ? cloneSessionTabDraft(tab) : emptyComposerDraft();
        if (tab?.id === this.state.activeSessionTabID) draft.prompt = this.draft;
        return draft;
      },
      restoreComposerDraftForThread: (id, draft) => {
        const tab = this.state.sessionTabs.find(tab => tab.id === id || tab.kind === "thread" && tab.threadID === id);
        if (!tab) return;
        if (tab.id === this.state.activeSessionTabID) { this.draft = draft.prompt; this.draftRevision++; }
        this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(item => item.id === tab.id && item.kind !== "skills" ? { ...item, ...draft } : item) }));
      },
      preserveFailedComposerMessage: (id, message) => this.updateState(state => updateThreadByID(state, id, thread => upsertTurn(thread, {
        ...createOptimisticTurn(message, Date.now()), status: "failed", error: { message: translateCurrent("composer.sendFailed") },
      }))),
      setStatus: status => this.updateState(state => ({ ...state, status })),
    });
    this.applicationNavigation = new ApplicationNavigation({ getState: () => this.state, updateState: this.updateState,
      changed: () => this.publish(), sidebarCache: this.sidebarCache, mutationActions: () => this.mutationActions(),
      getPendingDraftTabIds: () => [...this.pendingDraftAdmissions],
    });
    this.applicationPreview = new ApplicationPreview({ getThreads: () => [this.state.thread, this.state.secondaryThread, ...this.state.threads]
      .filter((thread): thread is Thread => Boolean(thread)), changed: () => this.publish() });
    this.applicationSettings = new ApplicationSettings({ getState: () => this.state, updateState: this.updateState,
      changed: () => this.publish(), isNavigationPending: () => Boolean(this.pendingNavigation && !this.pendingNavigation.contentReady),
      deleteArchivedThread: id => this.mutationActions().deleteArchivedThread(id),
      unarchiveThread: id => this.mutationActions().unarchiveThread({ id }) });
    const tab = activeSessionTab(this.state);
    this.draft = tab && tab.kind !== "skills" ? tab.prompt : "";
    this.rememberDraftResources();
    this.snapshot = this.project();
    this.sidebarCache.subscribe(() => this.publish());
    this.pendingComposer.subscribe(() => { this.rememberDraftResources(); this.publish(); });
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
    this.pendingComposer.reconcilePendingComposerMessagesForState(next);
    this.rememberDraftResources();
    this.publish();
    for (const listener of this.stateListeners) listener();
  };

  start(): () => void {
    if (++this.consumers === 1) {
      this.lifetime = new AbortController();
      this.subscriptions = [subscribeServerEvents(event => this.receive(event)), this.applicationNavigation.start(),
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
      if (loaded.thread && loaded.heldComposerMessages) this.pendingComposer.seedHeldComposerMessages(loaded.thread.id, loaded.heldComposerMessages);
      return loaded;
    })();
    return this.load;
  }

  private receive(event: ServerEvent): void {
    this.sidebarCache.syncSidebarServerEvent(event);
    this.pendingComposer.syncPendingComposerMessagesFromServerEvent(event);
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
    const references = new Set<string>();
    for (const tab of this.state.sessionTabs) {
      if (tab.kind === "skills") continue;
      [...tab.images, ...tab.files].forEach(attachment => references.add(attachment.id));
      tab.images.forEach(image => this.draftResources.remember("image", image));
      tab.files.forEach(file => this.draftResources.remember("file", file));
    }
    for (const pending of Object.values(this.pendingComposer.pendingComposerMessagesByThread)) {
      for (const message of [...pending.queued, ...pending.guides]) {
        message.images.forEach(image => { references.add(image.id); this.draftResources.remember("image", image); });
        message.files.forEach(file => { references.add(file.id); this.draftResources.remember("file", file); });
      }
    }
    this.draftResources.reconcile(references);
  }

  private composerDraftForPane(pane: "primary" | "secondary") {
    const state = this.state;
    const thread = pane === "primary" ? state.thread : state.secondaryThread;
    const tab = state.secondaryThread && thread
      ? state.sessionTabs.find(item => item.id === threadSessionTabID(thread.id)) : pane === "primary" ? activeSessionTab(state) : undefined;
    const draft = { ...(tab ? cloneSessionTabDraft(tab) : emptyComposerDraft()), ownerTabId: tab?.kind !== "skills" ? tab?.id : undefined };
    if (tab?.id === state.activeSessionTabID) draft.prompt = this.draft;
    return draft;
  }

  private beginNavigation(kind: NonNullable<ApplicationSnapshotV1["tabs"]["pending"]>["kind"], targetId: string,
    contextSwitching: boolean, signal?: AbortSignal): number {
    signal?.throwIfAborted();
    this.navigationAbortCleanup?.();
    const requestId = ++this.navigationRequest;
    const sourceContext = this.state.activeContext;
    this.pendingNavigation = Object.freeze({ kind, targetId, requestId, contextSwitching });
    const abort = () => {
      if (requestId !== this.navigationRequest) return;
      this.finishNavigation(requestId);
      this.navigationRequest++;
      if (signal && contextSwitching && sourceContext) this.abortedNavigation = { request: this.navigationRequest, signal, context: sourceContext };
    };
    signal?.addEventListener("abort", abort, { once: true });
    this.navigationAbortCleanup = () => signal?.removeEventListener("abort", abort);
    this.publish();
    return requestId;
  }

  private finishNavigation(requestId: number): boolean {
    if (requestId !== this.navigationRequest) return false;
    this.navigationAbortCleanup?.();
    this.navigationAbortCleanup = undefined;
    this.pendingNavigation = undefined;
    this.publish();
    return true;
  }

  private navigationActions(signal?: AbortSignal) {
    let restoredDraft: ComposerDraftState | undefined;
    const isCurrent = (request: number) => !signal?.aborted && request === this.navigationRequest;
    const common = {
      getAppState: () => this.state,
      getPendingViewSwitch: () => this.pendingNavigation && ({
        kind: this.pendingNavigation.kind === "tab" ? "thread" as const : this.pendingNavigation.kind,
        targetID: this.pendingNavigation.targetId, visible: true, contextSwitching: this.pendingNavigation.contextSwitching,
      }),
      getComposerDraftSnapshot: () => captureComposerDrafts(this.state, this.state.secondaryThread
        ? { primary: this.composerDraftForPane("primary"), secondary: this.composerDraftForPane("secondary") }
        : this.composerDraftForPane("primary")),
      setAppState: (update: SetStateAction<AppState>) => {
        signal?.throwIfAborted();
        this.updateState(state => {
          let next = typeof update === "function" ? update(state) : update;
          if (restoredDraft) {
            const draft = restoredDraft;
            restoredDraft = undefined;
            next = { ...next, sessionTabs: next.sessionTabs.map(tab => tab.id === next.activeSessionTabID && tab.kind !== "skills"
              ? { ...tab, ...draft } : tab) };
          }
          return next;
        });
      },
      restorePrimaryComposerDraft: (draft: ComposerDraftState) => { restoredDraft = draft; },
      clearPrimaryComposerDraft: () => { restoredDraft = emptyComposerDraft(); },
      // Split drafts already live in their owner tabs; accepted state changes
      // remove the secondary pane without a second mutable composer store.
      resetSplitComposerDrafts: () => {},
      nextDraftSessionTab: (context: NonNullable<AppState["activeContext"]>) => createDraftSessionTab(`draft:${crypto.randomUUID()}`, context),
      isDraftPending: (tabId: string) => this.pendingDraftAdmissions.has(tabId),
      beginViewSwitch: (kind: "thread" | "workspace" | "runtime", targetId: string, contextSwitching = false) => {
        return this.beginNavigation(kind, targetId, contextSwitching || kind !== "thread", signal);
      },
      prepareThreadReveal: (request: number) => {
        if (!isCurrent(request)) return false;
        this.pendingNavigation = this.pendingNavigation && Object.freeze({ ...this.pendingNavigation, contentReady: true });
        return true;
      },
      finishViewSwitch: (request: number) => {
        if (!isCurrent(request)) return false;
        return this.finishNavigation(request);
      },
      cancelViewSwitch: () => { this.finishNavigation(this.navigationRequest); this.navigationRequest++; },
      onError: (error: Error): never => { throw error; },
    };
    const threads = createThreadActivationActions({ ...common,
      getActiveThreadID: () => activeThreadIDForState(this.state),
      getSidebarThreads: () => [...this.state.threads, ...this.sidebarCache.getSnapshot().cachedScratchThreads],
      getSidebarWorkspaceThreadsByWorkspaceID: () => this.sidebarCache.getSnapshot().workspaceThreadsByWorkspaceID,
      getRunningThreadIDs: () => new Set(this.state.threads.filter(isThreadRunning).map(thread => thread.id)),
      isCurrentViewSwitchRequest: isCurrent,
    });
    const tabs = createSessionTabActions({ ...common, selectThread: threads.selectThread, selectContextThread: threads.selectContextThread });
    const workspaces = createWorkspaceRuntimeActions({ ...common,
      closeWorkspaceMenus: () => {},
      confirmRemoval: async () => true,
      restoreLoadedRuntimeComposerDraft: (loaded: Partial<AppState>, carry?: ComposerDraftState) => {
        if (carry) { restoredDraft = carry; return; }
        const next = withLoadedRuntimeSessionTab(this.state, loaded);
        const tab = activeSessionTab(next);
        restoredDraft = tab ? cloneSessionTabDraft(tab) : emptyComposerDraft();
      },
    });
    return { threads, tabs, workspaces };
  }

  private knownThread(id: string): Thread | undefined {
    const cache = this.sidebarCache.getSnapshot();
    return threadForTab(this.state, id) ?? cache.cachedScratchThreads.find(thread => thread.id === id)
      ?? Object.values(cache.workspaceThreadsByWorkspaceID).flat().find(thread => thread.id === id);
  }

  private mutationActions() {
    let restoredDraft: ComposerDraftState | undefined;
    return createThreadMutationActions({ getAppState: () => this.state,
      setAppState: update => this.updateState(state => {
        const next = typeof update === "function" ? update(state) : update;
        if (!restoredDraft) return next;
        const draft = restoredDraft;
        restoredDraft = undefined;
        return { ...next, sessionTabs: next.sessionTabs.map(tab => tab.id === next.activeSessionTabID && tab.kind !== "skills" ? { ...tab, ...draft } : tab) };
      }),
      nextDraftSessionTab: context => createDraftSessionTab(`draft:${crypto.randomUUID()}`, context),
      clearPrimaryComposerDraft: () => { restoredDraft = emptyComposerDraft(); },
      restorePrimaryComposerDraft: draft => { restoredDraft = draft; },
      getSplitComposerDrafts: () => ({ primary: this.composerDraftForPane("primary"), secondary: this.composerDraftForPane("secondary") }),
      resetSplitComposerDrafts: () => {},
      updateCachedSidebarThread: this.sidebarCache.updateCachedSidebarThread,
      updateCachedSidebarThreadPinned: this.sidebarCache.updateCachedSidebarThreadPinned,
      removeCachedSidebarThread: this.sidebarCache.removeCachedSidebarThread,
      clearThreadPendingComposerMessages: this.pendingComposer.clearThreadPendingComposerMessages,
      onError: error => { throw error; },
    });
  }

  private async selectTab(tab: SessionTab, signal?: AbortSignal): Promise<void> {
    const sourceContext = this.state.activeContext;
    const contextSwitching = !sameRuntimeContext(sourceContext, tab.context) || this.pendingNavigation?.contextSwitching === true;
    const request = this.beginNavigation("tab", tab.id, contextSwitching, signal);
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
      this.finishNavigation(request);
    }
  }

  async invoke<Action extends keyof ApplicationActionMapV1>(action: Action, input: ApplicationActionMapV1[Action]["input"], options?: { readonly signal?: AbortSignal }): Promise<ApplicationActionMapV1[Action]["result"]> {
    const signal = options?.signal;
    signal?.throwIfAborted();
    try {
      const result = await this.perform(action, input, signal);
      signal?.throwIfAborted();
      return result as ApplicationActionMapV1[Action]["result"];
    } finally {
      const recovery = this.abortedNavigation;
      if (signal?.aborted && recovery?.signal === signal && recovery.request === this.navigationRequest) {
        this.abortedNavigation = undefined;
        try { await selectRuntimeContext(recovery.context); }
        catch (error) {
          if (recovery.request === this.navigationRequest) this.updateState(state => ({ ...state,
            initialized: undefined, activeContext: undefined, activeProjectId: undefined, status: String(error) }));
        }
      }
    }
  }

  private async perform(action: keyof ApplicationActionMapV1, input: ApplicationActionMapV1[keyof ApplicationActionMapV1]["input"], signal?: AbortSignal): Promise<unknown> {
    if (isApplicationNavigationAction(action)) return this.applicationNavigation.invoke(action,
      input as ApplicationNavigationActionMapV1[keyof ApplicationNavigationActionMapV1]["input"], signal);
    if (isApplicationPreviewAction(action)) return this.applicationPreview.invoke(action,
      input as ApplicationPreviewActionMapV1[keyof ApplicationPreviewActionMapV1]["input"], signal);
    if (isApplicationSettingsAction(action)) return this.applicationSettings.invoke(action,
      input as ApplicationSettingsActionMapV1[keyof ApplicationSettingsActionMapV1]["input"], signal);
    switch (action) {
      case "conversation.activate-pane":
      case "conversation.close-pane": {
        const { pane } = input as ApplicationActionMapV1["conversation.activate-pane"]["input"];
        const panes = createConversationPaneActions({ setAppState: this.updateState,
          getComposerDraftSnapshot: () => captureComposerDrafts(this.state, this.state.secondaryThread
            ? { primary: this.composerDraftForPane("primary"), secondary: this.composerDraftForPane("secondary") }
            : this.composerDraftForPane("primary")),
          // Drafts are already stored in their thread tabs, including the surviving pane.
          moveSplitDraftToGlobalComposer: () => {},
        });
        if (action === "conversation.activate-pane") panes.activateConversationPane(pane);
        else panes.closeConversationPane(pane);
        return;
      }
      case "workspace.retarget-draft": {
        const { workspaceId } = input as ApplicationActionMapV1["workspace.retarget-draft"]["input"];
        const { workspaces } = this.navigationActions(signal);
        if (workspaceId) await workspaces.selectWorkspaceForNewThread(workspaceId);
        else await workspaces.useNoProject(false);
        return;
      }
      case "workspace.start-draft": {
        const { workspaceId } = input as ApplicationActionMapV1["workspace.start-draft"]["input"];
        const { workspaces } = this.navigationActions(signal);
        const activated = await (workspaceId ? workspaces.startNewThreadInWorkspace(workspaceId) : workspaces.useNoProject(true));
        return { activated, tabId: activated ? this.state.activeSessionTabID : undefined };
      }
      case "workspace.create": {
        const { kind } = input as ApplicationActionMapV1["workspace.create"]["input"];
        const { workspaces } = this.navigationActions(signal);
        await (kind === "blank" ? workspaces.createBlankProject() : workspaces.chooseProjectFolder()); return;
      }
      case "workspace.remove": {
        const { workspaceId } = input as ApplicationActionMapV1["workspace.remove"]["input"];
        await this.navigationActions(signal).workspaces.removeProject(workspaceId); return;
      }
      case "workspace.relocate": {
        const { workspaceId } = input as ApplicationActionMapV1["workspace.relocate"]["input"];
        await this.navigationActions(signal).workspaces.relocateProject(workspaceId); return;
      }
      case "thread.activate-child": {
        const { threadId } = input as ApplicationActionMapV1["thread.activate-child"]["input"];
        await this.navigationActions(signal).threads.selectChildAgent({ id: threadId }); return;
      }
      case "thread.activate": {
        const { threadId, workspaceId } = input as ApplicationActionMapV1["thread.activate"]["input"];
        const { threads } = this.navigationActions(signal);
        await (workspaceId ? threads.selectWorkspaceThread(workspaceId, threadId) : threads.activateThread(threadId)); return;
      }
      case "draft.start": {
        await this.navigationActions(signal).tabs.startNewThread();
        return { tabId: this.state.activeSessionTabID };
      }
      case "tab.pop-out": {
        const { tabId } = input as ApplicationActionMapV1["tab.pop-out"]["input"];
        await this.navigationActions(signal).tabs.popOutSessionTab(tabId); return;
      }
      case "navigation.reveal-complete": {
        const { requestId } = input as ApplicationActionMapV1["navigation.reveal-complete"]["input"];
        this.finishNavigation(requestId);
        return;
      }
      case "composer.import-attachments": {
        const { attachments } = input as ApplicationActionMapV1["composer.import-attachments"]["input"];
        this.draftResources.import(attachments); return;
      }
      case "composer.release-attachments": {
        const { attachmentIds, ifUnreferenced } = input as ApplicationActionMapV1["composer.release-attachments"]["input"];
        const referenced = new Set(this.state.sessionTabs.flatMap(tab => tab.kind === "skills" ? [] : [...tab.images, ...tab.files].map(item => item.id)));
        for (const pending of Object.values(this.pendingComposer.pendingComposerMessagesByThread)) {
          for (const message of [...pending.queued, ...pending.guides]) [...message.images, ...message.files].forEach(item => referenced.add(item.id));
        }
        if (!ifUnreferenced && attachmentIds.some(id => referenced.has(id))) throw new Error("Cannot release an attachment owned by a draft or pending message");
        this.draftResources.release(attachmentIds.filter(id => !referenced.has(id))); return;
      }
      case "composer.remove-pending": {
        const { messageId, kind } = input as ApplicationActionMapV1["composer.remove-pending"]["input"];
        return kind === "queue" ? this.pendingComposer.removeQueuedMessage(messageId) : this.pendingComposer.removeGuideMessage(messageId);
      }
      case "composer.edit-pending": {
        const { messageId, kind } = input as ApplicationActionMapV1["composer.edit-pending"]["input"];
        const message = Object.values(this.pendingComposer.pendingComposerMessagesByThread)
          .flatMap(pending => kind === "queue" ? pending.queued : pending.guides).find(message => message.id === messageId);
        const release = this.draftResources.retain(message ? [...message.images, ...message.files].map(item => item.id) : []);
        try { return await (kind === "queue" ? this.pendingComposer.editQueuedMessage(messageId) : this.pendingComposer.editGuideMessage(messageId)); }
        finally { release(); }
      }
      case "composer.toggle-pending-mode": return this.pendingComposer.guideQueuedMessage((input as ApplicationActionMapV1["composer.toggle-pending-mode"]["input"]).messageId);
      case "composer.attach": {
        const { files, ownerTabId = this.state.activeSessionTabID } = input as ApplicationActionMapV1["composer.attach"]["input"];
        const owner = this.state.sessionTabs.find(tab => tab.id === ownerTabId);
        if (!owner || owner.kind === "skills") throw new Error("Attachment draft owner is unavailable");
        const attachments = this.draftResources.attach(files);
        this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(tab => {
          if (tab.id !== ownerTabId || tab.kind === "skills") return tab;
          const draft = this.draftResources.read({ text: tab.prompt,
            imageIds: [...tab.images.map(image => image.id), ...attachments.filter(item => item.kind === "image").map(item => item.id)],
            fileIds: [...tab.files.map(file => file.id), ...attachments.filter(item => item.kind === "file").map(item => item.id)],
            selections: tab.selections ?? [] });
          return { ...tab, ...draft };
        }) }));
        this.draftRevision++;
        return Object.freeze({ attachments });
      }
      case "composer.resolve-attachment":
      case "composer.retry-attachment": {
        const { attachmentId } = input as ApplicationActionMapV1["composer.resolve-attachment"]["input"];
        return this.draftResources.resolve(attachmentId, action === "composer.retry-attachment");
      }
      case "composer.checkpoint": {
        const checkpoint = input as ApplicationActionMapV1["composer.checkpoint"]["input"];
        if (checkpoint.activeTabId !== this.state.activeSessionTabID) throw new Error("Draft owner changed");
        if (Boolean(this.state.secondaryThread) !== Boolean(checkpoint.secondary)
          || checkpoint.primary.ownerTabId !== undefined && checkpoint.primary.ownerTabId !== this.composerDraftForPane("primary").ownerTabId
          || checkpoint.secondary?.ownerTabId !== undefined && checkpoint.secondary.ownerTabId !== this.composerDraftForPane("secondary").ownerTabId) {
          throw new Error("Composer pane ownership changed");
        }
        const { primary, secondary } = this.draftResources.checkpoint(checkpoint.primary, checkpoint.secondary, checkpoint.attachments);
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
        const thread = this.knownThread(threadId);
        if (!thread) throw new Error("Thread is unavailable");
        await this.mutationActions().renameThread(thread, title); return;
      }
      case "thread.archive": {
        const { threadId, archived, force } = input as ApplicationActionMapV1["thread.archive"]["input"];
        if (!archived) { await this.mutationActions().unarchiveThread({ id: threadId }); return; }
        const thread = this.knownThread(threadId);
        if (!thread) throw new Error("Thread is unavailable");
        const result = await this.mutationActions().archiveThread(summarizeThreadsForSidebar([thread])[0], { force });
        if (!result.ok) throw new Error(result.error);
        return;
      }
      case "composer.set-draft": {
        const { text, ownerTabId = this.state.activeSessionTabID } = input as ApplicationActionMapV1["composer.set-draft"]["input"];
        if (ownerTabId === this.state.activeSessionTabID) this.setDraft(text);
        else {
          if (!this.state.sessionTabs.some(tab => tab.id === ownerTabId && tab.kind !== "skills")) throw new Error("Draft owner is unavailable");
          this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(tab => tab.id === ownerTabId && tab.kind !== "skills" ? { ...tab, prompt: text } : tab) }));
        }
        return;
      }
      case "composer.submit": {
        if (this.pendingNavigation && !this.pendingNavigation.contentReady) throw new Error("Wait for navigation to finish before submitting");
        const submission = input as ApplicationActionMapV1["composer.submit"]["input"];
        const sourceTabId = submission.ownerTabId ?? this.state.activeSessionTabID;
        if (this.submitting.has(sourceTabId)) throw new Error("A composer submission is already pending");
        const sourceTab = this.state.sessionTabs.find(tab => tab.id === sourceTabId);
        if (submission.ownerTabId !== undefined && (!sourceTab || sourceTab.kind === "skills")) throw new Error("Draft owner is unavailable");
        const draft = sourceTab ? cloneSessionTabDraft(sourceTab) : emptyComposerDraft();
        // Standalone shells can submit before their first tab has been created.
        if (sourceTabId === this.state.activeSessionTabID) draft.prompt = this.draft;
        const message = createComposerMessage(draft.prompt, draft.images, draft.files, undefined, draft.selections);
        if (!message) throw new Error("The composer draft is empty");
        const text = message.text;
        let draftRevision = this.draftRevision;
        const mode = submission.mode ?? "send";
        let thread = sourceTab?.kind === "thread" ? threadForTab(this.state, sourceTab.threadID) : sourceTab ? undefined : activeThreadForState(this.state);
        if (sourceTab?.kind === "thread" && !thread) throw new Error("Thread must be loaded before submitting");
        if (thread?.read_only) throw new Error("The selected thread is read-only");
        const context = thread ? resolveThreadRuntimeContext(thread, this.state.projects) : sourceTab?.context ?? this.state.activeContext;
        if (!thread && sourceTabId) {
          this.pendingDraftAdmissions.add(sourceTabId);
          this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(tab => tab.id === sourceTabId && tab.kind !== "skills" ? { ...tab, ...draft } : tab) }));
        }
        const submissionOwners = new Set([sourceTabId]);
        this.submitting.add(sourceTabId); this.publish();
        try {
          const images = message.images.length ? inputImagesFromComposer(await awaitComposerImages(message.images)) : undefined;
          const files = message.files.length ? inputFilesFromComposer(await awaitComposerFiles(message.files)) : undefined;
          signal?.throwIfAborted();
          if (!thread) {
            const created = await window.wuu.startThread(undefined, context);
            signal?.throwIfAborted();
            thread = created.thread;
            submissionOwners.add(threadSessionTabID(thread.id));
            this.submitting.add(threadSessionTabID(thread.id));
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
          const clientId = message.id;
          signal?.throwIfAborted();
          if (mode === "queue") {
            await window.wuu.queueTurn(thread.id, text, images, clientId, files, thread.permission_mode, undefined, message.contentParts, context);
            this.pendingComposer.enqueueComposerMessage(thread.id, { ...message, origin: "queue" });
          }
          else if (mode === "steer") {
            const turn = thread.turns.findLast(turn => turn.status === "in_progress");
            if (!turn) throw new Error("There is no running turn to steer");
            await window.wuu.steerTurn(thread.id, turn.id, text, images, clientId, files, undefined, message.contentParts, context);
            this.pendingComposer.updateThreadPendingComposerMessages(thread.id, pending => ({ ...pending,
              guides: [...pending.guides.filter(item => item.id !== message.id), { ...message, origin: "steer" }] }));
          } else {
            const result = await window.wuu.startTurn(thread.id, text, images, files, thread.permission_mode, undefined, message.contentParts, context, clientId);
            signal?.throwIfAborted();
            const threadId = thread.id;
            this.updateState(state => updateThreadByID(state, threadId, current => upsertTurn(current, result.turn)));
          }
          signal?.throwIfAborted();
          const sentThreadId = thread.id;
          const owner = this.state.sessionTabs.find(tab => tab.kind === "thread" && tab.threadID === sentThreadId);
          const activeOwner = owner?.id === this.state.activeSessionTabID;
          const unchangedOwner = owner && owner.kind !== "skills"
            && (activeOwner ? this.draft : owner.prompt) === draft.prompt
            && owner.images.map(image => image.id).join("\0") === draft.images.map(image => image.id).join("\0")
            && owner.files.map(file => file.id).join("\0") === draft.files.map(file => file.id).join("\0")
            && JSON.stringify(owner.selections ?? []) === JSON.stringify(draft.selections ?? []);
          if (unchangedOwner || !owner && this.draftRevision === draftRevision) {
            if (activeOwner || !owner) { this.draft = ""; this.draftRevision++; }
            this.updateState(state => ({ ...state, sessionTabs: state.sessionTabs.map(tab =>
              tab.kind === "thread" && tab.threadID === sentThreadId ? { ...tab, ...emptyComposerDraft() } : tab) }));
          }
        } finally { this.pendingDraftAdmissions.delete(sourceTabId); submissionOwners.forEach(id => this.submitting.delete(id)); this.publish(); }
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
    const projectPending = (message: QueuedComposerMessage) => {
      [...message.images, ...message.files].forEach(attachment => attachmentIds.add(attachment.id));
      return Object.freeze({ id: message.id, text: message.text, preview: queuedMessagePreview(message), fullPreview: queuedMessageFullPreview(message),
        imageIds: Object.freeze(message.images.map(image => image.id)), fileIds: Object.freeze(message.files.map(file => file.id)),
        held: message.held, deliveryUnconfirmed: message.deliveryUnconfirmed, heldPosition: message.heldPosition,
        origin: message.origin, operationState: message.operationState });
    };
    const composerPending = Object.freeze(Object.entries(this.pendingComposer.pendingComposerMessagesByThread).map(([threadId, messages]) =>
      Object.freeze({ threadId, queued: Object.freeze(messages.queued.map(projectPending)), guides: Object.freeze(messages.guides.map(projectPending)) })));
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
      navigationWorkspace: this.applicationNavigation.getSnapshot(),
      tabs: Object.freeze({ activeTabId: state.activeSessionTabID, activePane: state.activePane,
        primaryThreadId: state.thread?.id, secondaryThreadId: state.secondaryThread?.id,
        canSubmit: !this.submitting.has(state.activeSessionTabID) && (!this.pendingNavigation || this.pendingNavigation.contentReady === true) && !thread?.read_only,
        pending: this.pendingNavigation,
        items: Object.freeze(state.sessionTabs.map(tab => Object.freeze({ id: tab.id, kind: tab.kind,
          title: sessionTabLabel(tab, state), workspaceId: tab.context.kind === "project" ? tab.context.project_id : undefined,
          workspaceRoot: tab.context.cwd, threadId: tab.kind === "thread" ? tab.threadID : undefined,
          project: tab.kind === "draft" && tab.project === true,
          draftText: tab.kind === "skills" ? "" : tab.id === state.activeSessionTabID ? this.draft : tab.prompt,
          hasAttachments: tab.kind !== "skills" && (tab.images.length > 0 || tab.files.length > 0) }))) }),
      conversation: Object.freeze({ threadId: thread?.id, title: thread?.title || thread?.preview, turns: Object.freeze(turns), loading: !initialized }),
      composer: Object.freeze({ contractVersion: 1, draftText: this.draft, threadId: thread?.id, running, readOnly: thread?.read_only, availableSubmissionModes: Object.freeze(running ? ["queue", "steer"] as const : ["send"] as const),
        model: Object.freeze({ id: thread?.model || initialized?.model || "", label: thread?.model || initialized?.model || "", providerId: thread?.model_provider || initialized?.provider }),
        runtime: Object.freeze({ id: thread?.engine_id || "wuu", label: thread?.engine_id || "Wuu" }), permission: Object.freeze({ id: thread?.permission_mode || initialized?.permissions?.mode || "", label: thread?.permission_mode || initialized?.permissions?.mode || "" }), disabledReason: this.submitting.has(state.activeSessionTabID) ? "Submitting" : undefined }),
      composerPending,
      drafts: Object.freeze({ activeTabId: state.activeSessionTabID, activePane: state.activePane, split: Boolean(state.secondaryThread),
        primary: Object.freeze({ ...this.draftResources.project(primaryDraft), ownerTabId: primaryDraft.ownerTabId }), secondary: Object.freeze({ ...this.draftResources.project(secondaryDraft), ownerTabId: secondaryDraft.ownerTabId }),
        attachments: Object.freeze([...attachmentIds].map(id => this.draftResources.describe(id))) }),
      settings: Object.freeze({ ...this.applicationSettings.getSnapshot(), contractVersion: 1, open: this.settings.open, activePageId: this.settings.pageId,
        plugins: Object.freeze((initialized?.extension_inventory ?? []).filter(item => item.kind === "plugin").map(item => Object.freeze({ id: item.id, name: item.name, enabled: item.enabled !== false, status: item.runtime_state ?? item.state }))),
        providers: Object.freeze((initialized?.providers ?? []).map(provider => Object.freeze({ id: provider.name, label: provider.name, configured: provider.api_key_configured === true }))) }),
      preview: this.preview, previewWorkspace: this.applicationPreview.getSnapshot(),
      appearance: Object.freeze({ theme: this.theme, resolvedTheme: currentAppliedTheme(), uiFontSize: Number.parseFloat(document.documentElement.style.getPropertyValue("--conversation-message-font-size")) || MESSAGE_FLOW_FONT_SIZE_RANGE.default, codeFontSize: readAppearance().codeSize }),
      locale, status: Object.freeze({ contractVersion: 1, items: Object.freeze([Object.freeze({ id: "connection", label: state.status, kind: status === "error" ? "error" as const : "info" as const })]) }),
    });
  }
}

let desktopApplication: ApplicationController | undefined;
export function getDesktopApplication(): ApplicationController {
  return desktopApplication ??= new ApplicationController();
}
