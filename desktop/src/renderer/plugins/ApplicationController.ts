import type { SetStateAction } from "react";
import type { ApplicationActionMapV1, ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ConversationItemSnapshotV1 } from "../../../../packages/plugin-sdk/src/index";
import type { InitializeResult, PopOutInitResult, ServerEvent, Thread } from "../../shared/protocol";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../../shared/protocol";
import { activeSessionTab, activeThreadForState, resolveThreadRuntimeContext, sameRuntimeContext, reconcileResumedThreadTurns, handleStreamingNotification, initialState, isThreadRunning, reduceServerEvent,
  serverEventTargetsActiveContext, isCoalescedBackgroundThreadEvent, upsertThread, upsertTurn, updateThreadByID, withLoadedRuntimeSessionTab, type AppState } from "../AppState";
import { loadPopOutRuntime, loadRuntime, loadRuntimeConfiguration, selectRuntimeContext, loadRuntimeRestore, applyRuntimeRestore, type LoadedRuntimeState } from "../RuntimeLoadState";
import { readComposerDraftRecovery, sameRecoveryContext } from "../ComposerDraftRecovery";
import { readThreadReadState } from "../ThreadReadState";
import { subscribeServerEvents } from "../ServerEvents";
import { streamTextKey, streamTextStore } from "../StreamText";
import { getActiveLocale } from "../i18n";
import { applyThemePreference, currentAppliedTheme, observeAppliedTheme } from "../Theme";
import { observeAppearance, readAppearance, saveAppearance } from "../AppearancePreferences";
import { applyMessageFlowFontSize } from "../MessageFlowFontSizeSection";
import { toConversationItemSnapshot, toConversationProcessSnapshot } from "./ApplicationSnapshots";

export interface ApplicationShellSelection {
  readonly plugin_id: string;
  readonly generation: string;
  readonly provider_id: string;
}
export type ShellInitializeResult = InitializeResult & { application_shell?: ApplicationShellSelection | null };

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
  private submitting = false;
  private streamPublication?: number;

  constructor(state?: AppState) {
    this.state = state ?? { ...initialState, sessionTabs: this.recoveredDrafts.tabs, lastViewedTurnByThreadID: readThreadReadState() };
    this.snapshot = this.project();
  }

  getState = (): AppState => this.state;
  subscribeState = (listener: () => void): (() => void) => { this.stateListeners.add(listener); return () => { this.stateListeners.delete(listener); }; };
  getSnapshot = (): ApplicationSnapshotV1 => this.snapshot;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  updateState = (update: SetStateAction<AppState>): void => {
    this.flushBackgroundEvents();
    const next = typeof update === "function" ? update(this.state) : update;
    if (next === this.state) return;
    if (next.activeSessionTabID !== this.state.activeSessionTabID) {
      const tab = activeSessionTab(next);
      this.draft = tab && tab.kind !== "skills" ? tab.prompt : "";
      this.draftRevision++;
    }
    this.state = next;
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
    const activeTab = this.state.activeSessionTabID;
    const sessionTabs = this.state.sessionTabs.map(tab => tab.id === activeTab && tab.kind !== "skills" ? { ...tab, prompt: text } : tab);
    if (sessionTabs.some((tab, index) => tab !== this.state.sessionTabs[index])) this.updateState(state => ({ ...state, sessionTabs }));
    else this.publish();
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

  async invoke<Action extends keyof ApplicationActionMapV1>(action: Action, input: ApplicationActionMapV1[Action]["input"], options?: { readonly signal?: AbortSignal }): Promise<ApplicationActionMapV1[Action]["result"]> {
    const signal = options?.signal;
    signal?.throwIfAborted();
    const result = await this.perform(action, input, signal);
    signal?.throwIfAborted();
    return result as ApplicationActionMapV1[Action]["result"];
  }

  private async perform(action: keyof ApplicationActionMapV1, input: ApplicationActionMapV1[keyof ApplicationActionMapV1]["input"], signal?: AbortSignal): Promise<unknown> {
    switch (action) {
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
        if (this.submitting) throw new Error("A composer submission is already pending");
        const text = this.draft;
        let draftRevision = this.draftRevision;
        if (!text.trim()) throw new Error("The composer draft is empty");
        const mode = (input as ApplicationActionMapV1["composer.submit"]["input"]).mode ?? "send";
        let thread = activeThreadForState(this.state);
        if (thread?.read_only) throw new Error("The selected thread is read-only");
        const context = thread ? resolveThreadRuntimeContext(thread, this.state.projects) : this.state.activeContext;
        this.submitting = true; this.publish();
        try {
          if (!thread) {
            const created = await window.wuu.startThread(undefined, context);
            signal?.throwIfAborted();
            thread = created.thread;
            const currentDraft = this.draft;
            const unchanged = this.draftRevision === draftRevision;
            this.adoptThread(thread, true);
            // Moving a draft into its first thread must not acknowledge delivery
            // or discard text typed while that thread was being created.
            this.setDraft(currentDraft);
            if (unchanged) draftRevision = this.draftRevision;
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
        } finally { this.submitting = false; this.publish(); }
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
    }
  }

  private publish(): void {
    this.revision++;
    this.snapshot = this.project();
    for (const listener of this.listeners) listener();
  }

  private project(): ApplicationSnapshotV1 {
    const state = this.state;
    const initialized = state.initialized;
    const thread = activeThreadForState(state);
    const locale = getActiveLocale();
    const running = isThreadRunning(thread);
    const status = initialized ? "connected" : state.status === "connecting" ? "connecting" : "error";
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
      navigation: Object.freeze({ contractVersion: 1, activeNodeId: thread?.id, nodes: Object.freeze([
        ...state.projects.map(project => Object.freeze({ id: project.id, kind: "project" as const, label: project.name, active: state.activeProjectId === project.id })),
        ...state.threads.filter(thread => !thread.archived && !thread.ephemeral).map(item => Object.freeze({ id: item.id, kind: "thread" as const, label: item.title || item.preview || item.id, active: item.id === thread?.id, pinned: item.pinned, running: isThreadRunning(item) })),
      ]) }),
      conversation: Object.freeze({ threadId: thread?.id, title: thread?.title || thread?.preview, turns: Object.freeze(turns), loading: !initialized }),
      composer: Object.freeze({ contractVersion: 1, draftText: this.draft, threadId: thread?.id, running, readOnly: thread?.read_only, availableSubmissionModes: running ? ["queue", "steer"] as const : ["send"] as const,
        model: { id: thread?.model || initialized?.model || "", label: thread?.model || initialized?.model || "", providerId: thread?.model_provider || initialized?.provider },
        runtime: { id: thread?.engine_id || "wuu", label: thread?.engine_id || "Wuu" }, permission: { id: thread?.permission_mode || initialized?.permissions?.mode || "", label: thread?.permission_mode || initialized?.permissions?.mode || "" }, disabledReason: this.submitting ? "Submitting" : undefined }),
      settings: Object.freeze({ contractVersion: 1, open: this.settings.open, activePageId: this.settings.pageId,
        plugins: Object.freeze((initialized?.extension_inventory ?? []).filter(item => item.kind === "plugin").map(item => Object.freeze({ id: item.id, name: item.name, enabled: item.enabled !== false, status: item.runtime_state ?? item.state }))),
        providers: Object.freeze((initialized?.providers ?? []).map(provider => Object.freeze({ id: provider.name, label: provider.name, configured: provider.api_key_configured === true }))) }),
      preview: this.preview,
      appearance: Object.freeze({ theme: this.theme, resolvedTheme: currentAppliedTheme(), uiFontSize: Number.parseFloat(document.documentElement.style.getPropertyValue("--conversation-message-font-size")) || MESSAGE_FLOW_FONT_SIZE_RANGE.default, codeFontSize: readAppearance().codeSize }),
      locale, status: Object.freeze({ contractVersion: 1, items: Object.freeze([{ id: "connection", label: state.status, kind: status === "error" ? "error" as const : "info" as const }]) }),
    });
  }
}

let desktopApplication: ApplicationController | undefined;
export function getDesktopApplication(): ApplicationController {
  return desktopApplication ??= new ApplicationController();
}
