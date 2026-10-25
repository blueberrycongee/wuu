import type { ApplicationSubmissionInputV1, ApplicationSubmissionResultV1, ApplicationSubmissionSnapshotV1, ApplicationSubmissionOperationV1 } from "../../../../packages/plugin-sdk/src/ui-submission";
import type { MessageContentPart, ResponseSelection, RuntimeContext, Thread, ThreadStartParams, Turn } from "../../shared/protocol";
import { activeThreadIDForState, activeTurnIDForThread, activeTurnIsAnswerReady, bindActiveSessionTabToThread, cloneComposerDraft, composerDraftHasContent, createDraftSessionTab, createThreadSessionTab, emptyComposerDraft, isThreadRunning, resolveComposerRunningAction, resolveThreadRuntimeContext, sameRuntimeContext, setThreadForPane, threadForTab, threadSessionTabID, updateThreadByID, upsertThread, upsertTurn, type AppState, type ComposerDraftState, type SessionTab } from "../AppState";
import { awaitComposerFiles, awaitComposerImages, composerContextFromMessage, createComposerMessage, createOptimisticTurn, dropOptimisticTurn, inputFilesFromComposer, inputImagesFromComposer, interruptOptimisticTurn, isOptimisticTurnInterrupted, OPTIMISTIC_TURN_ID_PREFIX, replaceOptimisticTurn, threadHasAcceptedComposerMessage, type QueuedComposerMessage } from "../ComposerMessages";
import type { ComposerPendingStateController } from "../ComposerPendingState";
import type { ApplicationDraftResources } from "./ApplicationDrafts";
import type { createSidebarThreadCache } from "../SidebarWorkspaceState";
import { collectTurnArtifacts } from "../ArtifactOutputs";
import { createArtifactComposerFile } from "../ArtifactComposerFile";
import { customDraftConversationTitle } from "../ThreadTitles";
import { translateCurrent as t } from "../i18n";
import { isCancellationMessage, rawErrorMessage } from "../UserFacingErrors";
import { forgetLocalTurnTiming } from "../LocalTurnTiming";
import { rememberCollapsedPromptParts, readCollapsedPromptParts } from "../ComposerCollapsedPrompt";
import { writeComposerDraftRecovery } from "../ComposerDraftRecovery";
import { hasAuthenticatedProvider } from "../RuntimeHelpers";

export interface ApplicationSubmissionOptions {
  getState(): AppState;
  updateState(update: (state: AppState) => AppState): void;
  changed(): void;
  isNavigationPending(): boolean;
  pending: ComposerPendingStateController;
  resources: ApplicationDraftResources;
  getDraft(ownerTabId: string): ComposerDraftState;
  setDraft(ownerTabId: string, draft: ComposerDraftState): void;
  getRuntime(ownerTabId: string): { pending: boolean; params: ThreadStartParams; worktree?: { startBranch?: string } };
  sidebarCache: ReturnType<typeof createSidebarThreadCache>;
  consumeOffer?(offerID: string): Promise<void>;
}

type Operation = {
  view: ApplicationSubmissionOperationV1;
  message: QueuedComposerMessage;
  source: Exclude<SessionTab, { kind: "skills" }>;
  context: RuntimeContext;
  capturedContext?: boolean;
  thread?: Thread;
  optimistic: Turn;
  previousTurnIDs: ReadonlySet<string>;
  ready: Promise<Thread | undefined>;
  resolve(thread: Thread | undefined): void;
  cancelled: Promise<undefined>;
  cancelPreparation(): void;
  stopped: boolean;
  sent: boolean;
  creationCancelled: boolean;
  implicitFileID?: string;
  clickedAtMs: number;
  retainResult: boolean;
  draft?: ComposerDraftState;
  settlePlacement?: (accepted: boolean) => void;
};

/** One submission owner for all shells, sharing the controller's drafts and queues. */
export class ApplicationSubmission {
  private readonly operations = new Map<string, Operation>();
  private readonly results = new Map<string, ApplicationSubmissionOperationV1>();
  private readonly admissions = new Map<string, Operation>();
  private readonly continuations = new Set<string>();
  private readonly unadmitted = new Map<string, QueuedComposerMessage>();
  unadmittedComposerMessage = (id: string): QueuedComposerMessage | undefined => this.unadmitted.get(id);
  private readonly lanes = new Map<string, { tail: Promise<void>; stopVersion: number }>();
  private readonly creations = new Map<string, Operation>();
  private readonly stopRequests = new Map<string, { target?: string; phase: "pending" | "retry"; delivery?: symbol }>();
  private readonly failedDrafts = new Set<string>();
  private snapshot: ApplicationSubmissionSnapshotV1 = Object.freeze({ operations: Object.freeze([]), pendingCreations: Object.freeze([]), failedDraftTabIds: Object.freeze([]), stopRequests: Object.freeze([]) });
  private disposed = false;
  private readonly recoveryText = new Map<string, { prompt: string; text: string; hasReferences: boolean }>();
  private recoveryWarning = false;

  constructor(private readonly options: ApplicationSubmissionOptions) {}
  getSnapshot = (): ApplicationSubmissionSnapshotV1 => this.snapshot;
  getPendingDraftTabIds = (): readonly string[] => [...this.creations.keys()];
  isOwnerPending = (ownerTabId: string): boolean => {
    const tab = this.options.getState().sessionTabs.find(tab => tab.id === ownerTabId);
    return this.admissions.has(tab?.kind === "thread" ? tab.threadID : ownerTabId);
  };

  private publish(): void {
    this.snapshot = Object.freeze({ operations: Object.freeze([...this.results.values(), ...[...this.operations.values()].map(operation => operation.view)].map(view => Object.freeze({ ...view }))),
      pendingCreations: Object.freeze([...this.creations.values()].map(operation => {
        const turn = structuredClone(operation.optimistic);
        const freeze = (value: unknown): void => {
          if (!value || typeof value !== "object") return;
          Object.values(value).forEach(freeze);
          Object.freeze(value);
        };
        freeze(turn);
        return Object.freeze({ownerTabId: operation.source.id, operationID: operation.view.operationID,
          startedAtMs: operation.clickedAtMs, turn});
      })),
      failedDraftTabIds: Object.freeze([...this.failedDrafts]),
      stopRequests: Object.freeze([...this.stopRequests].map(([threadId, request]) => Object.freeze({ threadId, phase: request.phase }))) });
    // Admitted results still settle and release resources after disposal, but
    // notifying the ended owner would recreate its disposed projection services.
    if (!this.disposed) this.options.changed();
  }
  private phase(operation: Operation, phase: ApplicationSubmissionOperationV1["phase"], error?: string): void {
    operation.view = { ...operation.view, phase, threadId: operation.thread?.id, error }; this.publish();
  }

  submit(input: ApplicationSubmissionInputV1, signal?: AbortSignal): ApplicationSubmissionResultV1 {
    const state = this.options.getState();
    const source = state.sessionTabs.find(tab => tab.id === input.ownerTabId);
    const visible = input.ownerTabId === state.activeSessionTabID || Boolean(state.thread && input.ownerTabId === threadSessionTabID(state.thread.id)) || Boolean(state.secondaryThread && input.ownerTabId === threadSessionTabID(state.secondaryThread.id));
    if (this.disposed || signal?.aborted || this.options.isNavigationPending() || !source || source.kind === "skills" || !visible || !state.activeContext || !state.initialized) return { accepted: false };
    const thread = source.kind === "thread" ? threadForTab(state, source.threadID) : undefined;
    if (source.kind === "thread" && !thread || thread?.read_only || thread && this.stopRequests.has(thread.id) || !thread && this.options.getRuntime(source.id).pending) return { accepted: false };
    if (!thread && this.options.getRuntime(source.id).params.engine === "wuu" && !hasAuthenticatedProvider(state.initialized.providers)) return { accepted: false };
    if ([...this.operations.values()].some(operation => operation.view.draftPending && (operation.source.id === source.id || thread && operation.thread?.id === thread.id))) return { accepted: false };
    const draft = this.options.getDraft(source.id);
    const contentParts: MessageContentPart[] | undefined = input.contentParts ? structuredClone(input.contentParts) as MessageContentPart[] : input.text === undefined || input.text === draft.prompt ? readCollapsedPromptParts(thread?.id ?? source.id, draft.prompt) : undefined;
    const selections = input.selections ? structuredClone(input.selections) as ResponseSelection[] : draft.selections;
    let message = createComposerMessage(input.text ?? draft.prompt, draft.images, draft.files, contentParts, selections);
    if (!message) return { accepted: false };
    if (input.activeDocument) message = { ...message, activeDocument: { ...input.activeDocument } };
    let implicitFileID: string | undefined;
    if (input.artifact) {
      if (!thread || input.artifact.threadId !== thread.id) throw new Error("The artifact belongs to another conversation");
      const artifact = thread.turns.flatMap(collectTurnArtifacts).find(artifact => artifact.id === input.artifact!.id && artifact.sha256 === input.artifact!.sha);
      if (!artifact) throw new Error("The artifact snapshot is no longer available");
      const file = createArtifactComposerFile({ threadID: thread.id, artifact }, thread.id);
      implicitFileID = file.id;
      message = { ...message, files: [...message.files.filter(item => item.id !== file.id), file] };
    }
    // Native input can be ahead of the parent draft when Enter follows completion acceptance.
    // Delivery must compare against the text actually submitted, not its earlier render.
    if (input.text !== undefined && input.text !== draft.prompt) {
      draft.prompt = input.text;
      this.options.setDraft(source.id, draft);
    }
    const operation = this.createOperation(message, source, thread, draft); operation.implicitFileID = implicitFileID;
    message.images.forEach(value => this.options.resources.remember("image", value));
    message.files.forEach(value => this.options.resources.remember("file", value));
    const release = this.options.resources.retain([...message.images, ...message.files].map(value => value.id));
    const key = thread?.id ?? source.id;
    const admission = this.admissions.get(key);
    const busy = thread && isThreadRunning(thread) && !activeTurnIsAnswerReady(thread);
    const mode = input.mode ?? "send";
    const work = admission || !busy && this.lanes.has(key)
      ? this.queue(operation, admission)
      : busy
        ? thread && !admission && resolveComposerRunningAction(mode === "steer" ? "steer" : "queue", thread) === "steer"
          ? this.steer(operation) : this.queue(operation)
        : this.send(operation);
    // The draft remains the visible owner until delivery is confirmed.
    this.saveRecovery();
    if (input.consumedOfferID && this.options.consumeOffer) void this.options.consumeOffer(input.consumedOfferID).catch(error => this.report(error));
    const abort = () => { this.cancel(operation.view.operationID); };
    signal?.addEventListener("abort", abort, { once: true });
    void work.then(sent => {
      if (sent) this.releaseDraft(operation);
      else this.recover(operation);
      this.phase(operation, operation.stopped ? "cancelled" : sent ? "completed" : "failed", operation.view.error);
    }, error => { this.recover(operation); this.phase(operation, "failed", rawErrorMessage(error, t("composer.sendFailed"))); })
      .finally(() => { signal?.removeEventListener("abort", abort); release(); this.finishOperation(operation); this.saveRecovery(); });
    return { accepted: true, operationID: operation.view.operationID, threadId: thread?.id };
  }

  private createOperation(message: QueuedComposerMessage, source: Exclude<SessionTab, { kind: "skills" }>, thread?: Thread, draft?: ComposerDraftState): Operation {
    let resolve!: Operation["resolve"]; let cancelPreparation!: () => void;
    // A result belongs to its composing action, not an unbounded history log.
    for (const [id, view] of this.results) if (view.ownerTabId === source.id || thread && view.threadId === thread.id) this.results.delete(id);
    for (const active of this.operations.values()) if (active.source.id === source.id || thread && active.thread?.id === thread.id) active.retainResult = false;
    const clickedAtMs = Date.now();
    const operation: Operation = { retainResult: true, clickedAtMs, draft: draft && cloneComposerDraft(draft), view: { operationID: message.id, ownerTabId: source.id, threadId: thread?.id, messageID: message.id, phase: "preparing", ...(draft ? { draftPending: true } : {}) },
      message, source, context: thread ? resolveThreadRuntimeContext(thread, this.options.getState().projects) : source.context,
      thread, optimistic: createOptimisticTurn(message, clickedAtMs), previousTurnIDs: new Set(thread?.turns.map(turn => turn.id)),
      ready: new Promise(done => { resolve = done; }), resolve: thread => resolve(thread),
      cancelled: new Promise(done => { cancelPreparation = () => done(undefined); }), cancelPreparation: () => cancelPreparation(),
      stopped: false, sent: false, creationCancelled: false };
    this.operations.set(operation.view.operationID, operation); this.publish(); return operation;
  }

  cancel(operationID: string): boolean {
    const operation = this.operations.get(operationID);
    if (!operation || ["completed", "failed", "cancelled"].includes(operation.view.phase)) return false;
    operation.stopped = true; operation.cancelPreparation();
    if (this.creations.get(operation.source.id) === operation) { operation.creationCancelled = true; this.creations.delete(operation.source.id); }
    if (operation.thread && operation.sent) void this.interruptThread(operation.thread.id);
    this.publish(); return true;
  }

  private async queue(operation: Operation, admission?: Operation): Promise<boolean> {
    const key = operation.thread?.id ?? operation.source.id;
    forgetLocalTurnTiming(operation.optimistic.id);
    if (!operation.thread && !admission) return false;
    this.options.pending.enqueueComposerMessage(key, { ...operation.message, operationState: "preparing" });
    const lane = this.lanes.get(key) ?? { tail: Promise.resolve(), stopVersion: 0 };
    const previous = lane.tail; const version = lane.stopVersion;
    let release!: () => void;
    const tail = new Promise<void>(done => { release = done; }); lane.tail = tail; this.lanes.set(key, lane);
    try {
      await previous;
      if (admission) operation.thread = await admission.ready;
      if (!operation.thread) {
        const pendingKey = admission?.thread?.id ?? key;
        const exists = this.options.pending.pendingComposerMessagesForThread(pendingKey).queued.some(message => message.id === operation.message.id);
        this.options.pending.removePendingComposerMessageByID(pendingKey, operation.message.id, "queue");
        return !exists;
      }
      if (!operation.capturedContext) operation.context = resolveThreadRuntimeContext(operation.thread, this.options.getState().projects);
      return await this.deliverPending(operation, "queue", () => lane.stopVersion !== version || Boolean(admission?.stopped) || operation.stopped);
    } finally { release(); if (lane.tail === tail) { this.lanes.delete(key); if (admission?.thread) this.lanes.delete(admission.thread.id); } }
  }
  private async steer(operation: Operation): Promise<boolean> {
    forgetLocalTurnTiming(operation.optimistic.id);
    if (!operation.thread || !activeTurnIDForThread(operation.thread)) return false;
    this.options.pending.updateThreadPendingComposerMessages(operation.thread.id, previous => ({ ...previous, guides: [...previous.guides, { ...operation.message, origin: "steer", operationState: "preparing" }] }));
    return this.deliverPending(operation, "guide", () => false);
  }
  private async deliverPending(operation: Operation, kind: "queue" | "guide", shouldHold: () => boolean): Promise<boolean> {
    operation.view = { ...operation.view, mode: kind === "queue" ? "queue" : "steer" };
    this.publish();
    const thread = operation.thread!; const message = operation.message; const list = kind === "queue" ? "queued" : "guides";
    const exists = () => this.options.pending.pendingComposerMessagesForThread(thread.id)[list].some(candidate => candidate.id === message.id);
    try {
      const [images, files] = await Promise.all([awaitComposerImages(message.images), awaitComposerFiles(message.files)]);
      if (!exists()) return true;
      if (kind === "guide" && operation.stopped) { this.options.pending.removePendingComposerMessageByID(thread.id, message.id, kind); return false; }
      this.options.pending.updateThreadPendingComposerMessages(thread.id, previous => ({ ...previous, [list]: previous[list].map(candidate => candidate.id === message.id ? { ...candidate, operationState: "sending" } : candidate) }));
      this.phase(operation, "sending"); operation.sent = true;
      let acceptedID = message.id;
      if (kind === "queue") {
        const result = await window.wuu.queueTurn(thread.id, message.text.trim(), inputImagesFromComposer(images), message.id, inputFilesFromComposer(files), thread.permission_mode, message.activeDocument, message.contentParts, operation.context, shouldHold());
        acceptedID = result.queued.id || message.id;
      } else await window.wuu.steerTurn(thread.id, activeTurnIDForThread(thread)!, message.text.trim(), inputImagesFromComposer(images), message.id, inputFilesFromComposer(files), message.activeDocument, message.contentParts, operation.context);
      operation.view = { ...operation.view, messageID: acceptedID };
      this.options.pending.updateThreadPendingComposerMessages(thread.id, previous => ({ ...previous, [list]: previous[list].map(candidate => candidate.id === message.id ? { ...candidate, id: acceptedID, images, files, operationState: undefined } : candidate) }));
      if (operation.view.draftPending && !this.disposed && this.resultOwnerExists(operation.view)) {
        return await new Promise<boolean>(resolve => {
          operation.settlePlacement = resolve;
          this.reconcile();
        });
      }
      return true;
    } catch (error) {
      // A server-owned uncertain delivery must stay behind the canonical
      // pending owner's retry/edit barrier; a timeout is not a rejection.
      const latest = this.options.pending.pendingComposerMessagesForThread(thread.id)[list].find(candidate => candidate.id === message.id);
      if (latest?.deliveryUnconfirmed) return true;
      const pending = exists(); this.options.pending.removePendingComposerMessageByID(thread.id, message.id, kind);
      if (pending) this.phase(operation, "failed", rawErrorMessage(error, t(kind === "queue" ? "app.queueFailed" : "composer.guideFailed")));
      return !pending;
    }
  }

  private async send(operation: Operation, prepareThread?: () => Promise<Thread>): Promise<boolean> {
    const { source, context, message, optimistic } = operation;
    const initial = this.options.getState(); const target = operation.thread;
    const runtime: ReturnType<ApplicationSubmissionOptions["getRuntime"]> = target
      ? { pending: false, params: { engine: target.engine_id, permission_mode: target.permission_mode } }
      : this.options.getRuntime(source.id);
    if (!target && (runtime.pending || (runtime.params.engine ?? "wuu") === "wuu" && !hasAuthenticatedProvider(initial.initialized?.providers))) { forgetLocalTurnTiming(optimistic.id); return false; }
    const key = target?.id ?? source.id;
    if (this.admissions.has(key) || target?.read_only || target && isThreadRunning(target) && !activeTurnIsAnswerReady(target)) { forgetLocalTurnTiming(optimistic.id); return false; }
    this.unadmitted.set(message.id, message);
    operation.view = { ...operation.view, mode: "send" };
    this.admissions.set(key, operation);
    if (!target) this.creations.set(source.id, operation);
    if (!operation.draft) this.options.updateState(state => state.activeSessionTabID === source.id ? { ...state, running: true, status: "" } : state);
    this.publish();
    let optimisticThreadID: string | undefined;
    try {
      const projectDraft = !target && source.kind === "draft" && source.project && initial.initialized?.features?.project_agent && context.kind === "project";
      let thread = prepareThread ? await prepareThread() : target;
      if (!thread) {
        let params = { ...runtime.params };
        if (runtime.worktree) params = { ...params, workspace: "worktree", ...(runtime.worktree.startBranch ? { base_revision: runtime.worktree.startBranch } : {}) };
        if (projectDraft && context.kind === "project") {
          const line = message.text.split("\n").find(line => line.trim())?.trim().replace(/\s+/g, " ") ?? "";
          // Project coordinators use the server's default permission policy,
          // not the ordinary workspace composer's permission selection.
          params = { engine: "wuu", provider: runtime.params.provider, model: runtime.params.model,
            effort: runtime.params.effort, speed: runtime.params.speed, workspace_id: context.project_id, cwd: context.cwd,
            project: { name: customDraftConversationTitle(source.kind === "draft" ? source.title : undefined, t("projects.newProject")) || (line.length > 48 ? `${line.slice(0, 47)}…` : line) } };
        }
        const creating = window.wuu.startThread(params, context).then(async result => {
          if (operation.creationCancelled && result.thread) {
            try {
              await window.wuu.deleteThread(result.thread.id);
              this.options.sidebarCache.removeCachedSidebarThread(result.thread.id);
              this.options.updateState(state => ({ ...state, threads: state.threads.filter(thread => thread.id !== result.thread!.id) }));
            } catch (error) { this.report(error); }
          }
          return result.thread;
        });
        thread = await Promise.race([creating, operation.cancelled]);
        if (!thread) {
          if (!operation.stopped) throw new Error("thread/start did not return a thread");
          return false;
        }
      }
      // An edit's authoritative prepare result is adopted even if Stop arrived.
      operation.thread = thread;
      const customTitle = !target && !projectDraft && !operation.creationCancelled
        ? customDraftConversationTitle(source.kind === "draft" ? source.title : undefined, t("tabs.newConversation")) : "";
      const originalTitle = thread.title;
      if (customTitle) thread = { ...thread, title: customTitle };
      operation.thread = thread;
      // State publication may synchronously reconcile owners during binding.
      operation.view = { ...operation.view, threadId: thread.id };
      if (!target) {
        this.admissions.set(thread.id, operation); this.admissions.delete(key);
        const lane = this.lanes.get(key);
        if (lane) { this.lanes.set(thread.id, lane); this.lanes.delete(key); }
        const pending = this.options.pending.pendingComposerMessagesByThread[key];
        if (pending) {
          this.options.pending.updateThreadPendingComposerMessages(thread.id, previous => ({ ...previous, queued: [...previous.queued, ...pending.queued], guides: [...previous.guides, ...pending.guides] }));
          this.options.pending.clearThreadPendingComposerMessages(key);
        }
        const draft = this.options.getDraft(source.id);
        rememberCollapsedPromptParts(thread.id, draft.prompt, readCollapsedPromptParts(source.id, draft.prompt));
        const created = thread;
        this.options.updateState(state => {
          const stillTarget = state.activeSessionTabID === source.id && sameRuntimeContext(state.activeContext, context);
          return { ...(stillTarget ? setThreadForPane(state, state.activePane, created) : state),
            sessionTabs: bindActiveSessionTabToThread(state.sessionTabs, source.id, created, context),
            activeSessionTabID: stillTarget ? threadSessionTabID(created.id) : state.activeSessionTabID, threads: upsertThread(state.threads, created) };
        });
      }
      optimisticThreadID = thread.id;
      if (!operation.draft) this.options.updateState(state => updateThreadByID(state, thread!.id, current => upsertTurn(current, optimistic)));
      // Event/RPC confirmation binds the send clock even without a visible placeholder.
      this.creations.delete(source.id); this.publish();
      if (customTitle) {
        const renamed = window.wuu.renameThread(thread.id, customTitle).catch(error => {
          const revert = (current: Thread): Thread => current.title === customTitle ? { ...current, title: originalTitle } : current;
          this.options.sidebarCache.patchCachedSidebarThread(thread!.id, revert);
          this.options.updateState(state => {
            const next = updateThreadByID(state, thread!.id, revert);
            return { ...next, sessionTabs: next.sessionTabs.map(tab => tab.kind === "thread" && tab.threadID === thread!.id && tab.title === customTitle ? { ...tab, title: originalTitle || t("thread.untitled") } : tab) };
          });
          this.report(error);
        });
        await Promise.race([renamed, operation.cancelled]);
      }
      const attachments = await Promise.race([Promise.all([awaitComposerImages(message.images), awaitComposerFiles(message.files)]), operation.cancelled]);
      if (!attachments || operation.stopped) {
        if (operation.draft) forgetLocalTurnTiming(optimistic.id);
        this.options.updateState(state => updateThreadByID(state, thread!.id, current => interruptOptimisticTurn(current, optimistic.id, Date.now()), activeThreadIDForState(state) === thread!.id ? { running: false } : {}));
        return false;
      }
      operation.sent = true; this.phase(operation, "sending");
      const [images, files] = attachments;
      const result = await window.wuu.startTurn(thread.id, message.text.trim(), inputImagesFromComposer(images), inputFilesFromComposer(files), thread.permission_mode || runtime.params.permission_mode, message.activeDocument, message.contentParts, context, message.id);
      this.unadmitted.delete(message.id);
      this.releaseDraft(operation);
      this.options.updateState(state => updateThreadByID(state, thread!.id, current => replaceOptimisticTurn(current, optimistic.id, result.turn, upsertTurn)));
      if (operation.stopped && isThreadRunning(threadForTab(this.options.getState(), thread.id))) await this.interruptAccepted(thread.id);
      return true;
    } catch (error) {
      const raw = rawErrorMessage(error, t("composer.sendFailed"));
      const current = optimisticThreadID ? threadForTab(this.options.getState(), optimisticThreadID) : undefined;
      const interrupted = isCancellationMessage(raw.toLowerCase()) || isOptimisticTurnInterrupted(current, optimistic.id);
      const accepted = threadHasAcceptedComposerMessage(current, message, optimistic.id, operation.previousTurnIDs);
      if (operation.draft && interrupted && !accepted) forgetLocalTurnTiming(optimistic.id);
      const busy = `thread ${JSON.stringify(optimisticThreadID)} already has a running turn`;
      if (!interrupted && !accepted && operation.sent && operation.thread && (raw.endsWith(busy) || raw.endsWith(`${busy} in another app-server: thread execution is owned by another app-server`))) {
        this.options.updateState(state => updateThreadByID(state, operation.thread!.id, current => dropOptimisticTurn(current, optimistic.id)));
        this.options.pending.enqueueComposerMessage(operation.thread.id, { ...message, operationState: "preparing" });
        return await this.deliverPending(operation, "queue", () => operation.stopped);
      }
      const keep = accepted && !interrupted;
      this.options.updateState(state => {
        const next = optimisticThreadID ? updateThreadByID(state, optimisticThreadID, current => interrupted ? interruptOptimisticTurn(current, optimistic.id, Date.now()) : keep ? current : dropOptimisticTurn(current, optimistic.id)) : state;
        const stillTarget = optimisticThreadID ? activeThreadIDForState(state) === optimisticThreadID : state.activeSessionTabID === source.id;
        return stillTarget ? { ...next, running: keep, status: "" } : next;
      });
      if (!interrupted && !keep) this.phase(operation, "failed", raw);
      if (operation.sent && optimisticThreadID && this.stopRequests.has(optimisticThreadID)) await this.interruptAccepted(optimisticThreadID);
      return !operation.creationCancelled && (interrupted || keep);
    } finally {
      operation.resolve(operation.thread);
      if (this.admissions.get(key) === operation) this.admissions.delete(key);
      if (operation.thread && this.admissions.get(operation.thread.id) === operation) this.admissions.delete(operation.thread.id);
      this.creations.delete(source.id);
      if (!optimisticThreadID) {
        forgetLocalTurnTiming(optimistic.id);
        this.options.updateState(state => state.activeSessionTabID === source.id ? { ...state, running: false } : state);
      }
      this.publish(); this.reconcile();
    }
  }

  async stop(ownerTabId: string): Promise<void> {
    const state = this.options.getState(); const tab = state.sessionTabs.find(tab => tab.id === ownerTabId);
    const creation = this.creations.get(ownerTabId);
    if (creation) { this.cancel(creation.view.operationID); return; }
    if (tab?.kind !== "thread") return;
    await this.interruptThread(tab.threadID);
  }
  async interruptThread(threadID: string): Promise<void> {
    const thread = threadForTab(this.options.getState(), threadID); if (!thread) return;
    const admission = this.admissions.get(thread.id);
    if (admission) { admission.stopped = true; if (!admission.sent) admission.cancelPreparation(); }
    const lane = this.lanes.get(thread.id); if (lane) lane.stopVersion++;
    if (this.stopRequests.get(thread.id)?.phase === "pending") return;
    this.stopRequests.set(thread.id, { target: thread.turns.at(-1)?.id, phase: "pending" }); this.publish();
    if (admission && !thread.turns.some(turn => turn.status === "in_progress" && !turn.answer_ready_at && !turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX))) return;
    await this.interruptAccepted(thread.id);
  }
  private async interruptAccepted(threadID: string): Promise<void> {
    const request = this.stopRequests.get(threadID) ?? { phase: "pending" as const };
    if (request.delivery) return;
    const delivery = Symbol(); this.stopRequests.set(threadID, { ...request, delivery });
    try {
      const result = await window.wuu.interruptTurn(threadID);
      if (!result.ok) throw new Error(t("composer.stopUnconfirmed"));
    } catch (error) {
      if (this.stopRequests.get(threadID)?.delivery !== delivery) return;
      this.stopRequests.set(threadID, { ...request, phase: "retry", delivery: undefined }); this.publish(); this.report(error);
    }
  }
  reconcile(removedMessageID?: string): void {
    const beforeStops = this.stopRequests.size; const beforeFailures = this.failedDrafts.size; const beforeResults = this.results.size;
    const state = this.options.getState();
    for (const [threadID, request] of this.stopRequests) {
      const thread = threadForTab(state, threadID);
      const targetEnded = thread?.turns.some(turn => turn.status !== "in_progress" && (turn.id === request.target || turn.items.some(item => item.type === "user_message" && item.source_id && `${OPTIMISTIC_TURN_ID_PREFIX}${item.source_id}` === request.target)));
      if (thread && !isThreadRunning(thread) && !this.admissions.has(threadID) && (request.phase !== "retry" || targetEnded)) this.stopRequests.delete(threadID);
      else if (request.phase === "pending" && thread?.turns.some(turn => turn.status === "in_progress" && !turn.answer_ready_at && !turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX))) void this.interruptAccepted(threadID);
    }
    for (const [id, view] of this.results) if (!this.resultOwnerExists(view)) this.results.delete(id);
    for (const operation of this.operations.values()) {
      const thread = operation.thread && threadForTab(state, operation.thread.id);
      if (operation.view.phase === "sending" && operation.view.mode === "send" && thread
        && threadHasAcceptedComposerMessage(thread, operation.message, operation.optimistic.id, operation.previousTurnIDs)) {
        this.phase(operation, "completed");
      }
      if (operation.view.draftPending && operation.thread) {
        const accepted = thread?.turns.some(turn => !turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX) && turn.items.some(item => item.type === "user_message" && item.source_id === operation.view.messageID));
        const pending = this.options.pending.pendingComposerMessagesForThread(operation.thread.id);
        const message = [...pending.queued, ...pending.guides].find(row => row.id === operation.view.messageID);
        if (accepted || operation.settlePlacement && (message?.held || message?.deliveryUnconfirmed)) {
          this.releaseDraft(operation);
          operation.settlePlacement?.(true);
          operation.settlePlacement = undefined;
        } else if (operation.settlePlacement && operation.view.messageID === removedMessageID && !message) {
          operation.stopped = true;
          operation.settlePlacement(false);
          operation.settlePlacement = undefined;
        }
      }
      if (this.resultOwnerExists(operation.view)) continue;
      operation.retainResult = false;
      operation.settlePlacement?.(true);
      operation.settlePlacement = undefined;
      // Background thread requests still have a real thread owner. Only a
      // removed draft's not-yet-admitted creation must be cancelled here.
      if (!operation.thread && !operation.sent) this.cancel(operation.view.operationID);
    }
    this.saveRecovery();
    for (const id of this.failedDrafts) if (id === state.activeSessionTabID || !state.sessionTabs.some(tab => tab.id === id)) this.failedDrafts.delete(id);
    if (beforeStops !== this.stopRequests.size || beforeFailures !== this.failedDrafts.size || beforeResults !== this.results.size) this.publish();
  }
  private resultOwnerExists(view: ApplicationSubmissionOperationV1): boolean {
    const state = this.options.getState();
    return state.sessionTabs.some(tab => tab.id === view.ownerTabId || tab.kind === "thread" && tab.threadID === view.threadId);
  }
  private finishOperation(operation: Operation): void {
    // No terminal receipt retains the message, File, byte preparation, source
    // draft, or Thread graph. Canonical drafts/pending messages keep recovery.
    this.operations.delete(operation.view.operationID);
    if (!this.disposed && operation.retainResult && this.resultOwnerExists(operation.view)) {
      this.results.set(operation.view.operationID, Object.freeze({ ...operation.view }));
    }
    this.publish();
  }
  private ownsDraft(operation: Operation, owner: string): boolean {
    const current = this.options.getDraft(owner);
    const draft = operation.draft;
    return Boolean(draft && current.prompt === draft.prompt
      && JSON.stringify(current.images.map(row => row.id)) === JSON.stringify(draft.images.map(row => row.id))
      && JSON.stringify(current.files.map(row => row.id)) === JSON.stringify(draft.files.map(row => row.id))
      && JSON.stringify(current.selections ?? []) === JSON.stringify(draft.selections ?? []));
  }
  private releaseDraft(operation: Operation): void {
    if (!operation.view.draftPending) return;
    operation.view = { ...operation.view, draftPending: false };
    const owner = operation.thread ? threadSessionTabID(operation.thread.id) : operation.source.id;
    // Another shell or an edit made while waiting owns its new content.
    if (this.ownsDraft(operation, owner)) this.options.setDraft(owner, emptyComposerDraft());
    this.publish();
  }
  private recover(operation: Operation): void {
    const { message, source } = operation;
    const retainedOwner = operation.thread ? threadSessionTabID(operation.thread.id) : source.id;
    if (operation.view.draftPending && this.ownsDraft(operation, retainedOwner)) {
      operation.view = { ...operation.view, draftPending: false };
      return;
    }
    operation.view = { ...operation.view, draftPending: false };
    const draft = { ...composerContextFromMessage(message.text, message.contentParts), images: message.images, files: message.files.filter(file => file.id !== operation.implicitFileID) };
    const owner = operation.thread ? threadSessionTabID(operation.thread.id) : source.id;
    const state = this.options.getState();
    if (state.sessionTabs.some(tab => tab.id === owner) && !composerDraftHasContent(this.options.getDraft(owner))) {
      rememberCollapsedPromptParts(operation.thread?.id ?? owner, draft.prompt, draft.contentParts);
      this.options.setDraft(owner, cloneComposerDraft(draft)); return;
    }
    if (operation.thread) this.options.updateState(state => updateThreadByID(state, operation.thread!.id, current => upsertTurn(current, { ...createOptimisticTurn(message, Date.now()), status: "failed", error: { message: operation.view.error || t("composer.sendFailed") } })));
    const recovery = createDraftSessionTab(`draft:recovery:${message.id}`, operation.context, draft);
    rememberCollapsedPromptParts(recovery.id, draft.prompt, draft.contentParts);
    this.options.updateState(state => ({ ...state, sessionTabs: [recovery, ...state.sessionTabs] }));
    this.failedDrafts.add(recovery.id);
  }
  private saveRecovery(): void {
    const state = this.options.getState();
    if (!state.initialized || !state.activeContext) return;
    const tabs = new Map(state.sessionTabs.map(tab => [tab.id, tab]));
    for (const [id, tab] of tabs) {
      if (tab.kind === "skills") continue;
      const draft = this.options.getDraft(id);
      const parts = readCollapsedPromptParts(tab.kind === "thread" ? tab.threadID : id, draft.prompt);
      const previous = this.recoveryText.get(id);
      const hasReferences = parts ? parts.some(part => part.type !== "text" && part.type !== "pasted_text")
        : Boolean(previous?.hasReferences && draft.prompt && draft.prompt !== previous.text);
      const text = parts ? parts.filter(part => part.type === "text" || part.type === "pasted_text").map(part => part.text).join("")
        : previous?.prompt === draft.prompt ? previous.text : hasReferences ? "" : draft.prompt;
      this.recoveryText.set(id, { prompt: draft.prompt, text, hasReferences });
      tabs.set(id, { ...tab, ...draft, prompt: text });
    }
    for (const id of this.recoveryText.keys()) if (!tabs.has(id)) this.recoveryText.delete(id);
    for (const operation of this.operations.values()) {
      if (operation.view.phase !== "preparing" && operation.view.phase !== "sending") continue;
      const parts = operation.message.contentParts;
      const prompt = parts ? parts.filter(part => part.type === "text" || part.type === "pasted_text").map(part => part.text).join("") : operation.message.text;
      const id = operation.thread ? threadSessionTabID(operation.thread.id) : operation.source.id;
      const live = tabs.get(id);
      if (!live || live.kind === "skills") continue;
      if (!live.prompt.trim()) tabs.set(id, { ...live, prompt });
      else if (live.prompt !== prompt) {
        const recovery = createDraftSessionTab(`draft:recovery:${operation.message.id}`, operation.context, { ...emptyComposerDraft(), prompt });
        tabs.set(recovery.id, recovery);
      }
    }
    const saved = writeComposerDraftRecovery([...tabs.values()], state.activeSessionTabID);
    const warn = !saved && !this.recoveryWarning;
    this.recoveryWarning = !saved;
    if (warn) this.report(t("settings.draftRecoveryUnavailable"));
  }
  private report(error: unknown): void { this.options.updateState(state => ({ ...state, status: rawErrorMessage(error, t("composer.sendFailed")) })); }

  private async runInternal(operation: Operation, kind: "send" | "steer" | "background", prepareThread?: () => Promise<Thread>): Promise<boolean> {
    const { message, thread } = operation;
    message.images.forEach(value => this.options.resources.remember("image", value));
    message.files.forEach(value => this.options.resources.remember("file", value));
    const release = this.options.resources.retain([...message.images, ...message.files].map(value => value.id));
    const key = thread?.id ?? operation.source.id;
    const admission = this.admissions.get(key);
    try {
      const sent = await (kind === "steer" ? this.steer(operation)
        : kind === "background" && (admission || this.lanes.has(key) || thread && isThreadRunning(thread) && !activeTurnIsAnswerReady(thread))
          ? this.queue(operation, admission) : this.send(operation, prepareThread));
      this.phase(operation, operation.stopped ? "cancelled" : sent ? "completed" : "failed", operation.view.error);
      return sent;
    } finally { release(); this.finishOperation(operation); }
  }
  private submitInternal(message: QueuedComposerMessage, thread: Thread, kind: "send" | "steer" | "background", prepareThread?: () => Promise<Thread>, context?: RuntimeContext): Promise<boolean> {
    if (this.disposed || thread.read_only || kind !== "background" && !threadForTab(this.options.getState(), thread.id)) return Promise.resolve(false);
    const source = this.options.getState().sessionTabs.find(tab => tab.kind === "thread" && tab.threadID === thread.id)
      ?? createThreadSessionTab(thread, context ?? resolveThreadRuntimeContext(thread, this.options.getState().projects));
    if (source.kind === "skills") return Promise.resolve(false);
    const operation = this.createOperation(message, source, thread);
    if (context) { operation.context = { ...context }; operation.capturedContext = true; }
    return this.runInternal(operation, kind, prepareThread);
  }
  async continueThread(thread: Thread, sourceTurnID: string): Promise<boolean> {
    if (this.disposed || thread.read_only || thread.archived || isThreadRunning(thread)
      || sourceTurnID.startsWith(OPTIMISTIC_TURN_ID_PREFIX) || thread.turns.at(-1)?.id !== sourceTurnID
      || !["failed", "interrupted"].includes(thread.turns.at(-1)?.status ?? "")
      || this.stopRequests.has(thread.id) || this.admissions.has(thread.id) || this.continuations.has(thread.id)) return false;
    this.continuations.add(thread.id);
    try {
      const result = await window.wuu.startTurn(thread.id, "", undefined, undefined, thread.permission_mode,
        undefined, undefined, resolveThreadRuntimeContext(thread, this.options.getState().projects), undefined, sourceTurnID);
      this.options.updateState(state => updateThreadByID(state, thread.id, current => upsertTurn(current, result.turn)));
      return true;
    } finally {
      this.continuations.delete(thread.id);
    }
  }
  submitToThread(message: QueuedComposerMessage, thread: Thread, prepareThread?: () => Promise<Thread>): Promise<boolean> {
    return this.submitInternal(message, thread, "send", prepareThread);
  }
  steerToThread(message: QueuedComposerMessage, thread: Thread): Promise<boolean> {
    return this.submitInternal(message, thread, "steer");
  }
  submitBackground(message: QueuedComposerMessage, thread: Thread, context: RuntimeContext): Promise<boolean> {
    return this.submitInternal(message, thread, "background", undefined, context);
  }
  submitIndependent(ownerTabId: string, message: QueuedComposerMessage): Promise<boolean> {
    const state = this.options.getState();
    const source = state.sessionTabs.find(tab => tab.id === ownerTabId);
    if (this.disposed || this.options.isNavigationPending() || !state.initialized || !source || source.kind === "skills") return Promise.resolve(false);
    const thread = source.kind === "thread" ? threadForTab(state, source.threadID) : undefined;
    if (source.kind === "thread" && !thread || thread?.read_only || !thread && this.options.getRuntime(ownerTabId).pending) return Promise.resolve(false);
    return this.runInternal(this.createOperation(message, source, thread), "background");
  }
  dispose(): void {
    this.disposed = true;
    this.results.clear();
    this.unadmitted.clear();
    for (const operation of this.operations.values()) {
      if (!operation.sent) this.cancel(operation.view.operationID);
      // A confirmed queue owns recovery once the composing service shuts down.
      if (operation.settlePlacement) this.releaseDraft(operation);
      operation.settlePlacement?.(true);
      operation.settlePlacement = undefined;
    }
  }
}
