import type { SetStateAction } from "react";
import type { ApplicationConversationActionMapV1 as Actions, ApplicationConversationActionsViewV1, ConversationEditDraftV1, ConversationHistoryTargetV1, ConversationQuestionAnswerV1, ConversationReadonly } from "../../../../packages/plugin-sdk/src/ui-conversation-actions";
import type { InputImage, InputFile, MessageContentPart, ServerEvent, Thread, UserQuestionAnswer, UserQuestionRequest } from "../../shared/protocol";
import { createConversationHistoryActions, type HistoryMessageEditState, type PendingForkState } from "../ConversationHistoryActions";
import { activeThreadForState, createThreadSessionTab, ensureSessionTab, isThreadRunning, requireThread, sameRuntimeContext, setThreadForPane, threadForTab, threadSessionTabID, updateThreadByID, upsertThread, upsertTurn, type AppState, type ComposerDraftState, type ConversationPaneID } from "../AppState";
import { createComposerMessage, createOptimisticCompactTurn, failOptimisticCompactTurn, replaceOptimisticTurn, type QueuedComposerMessage } from "../ComposerMessages";
import { subscribeServerEvents } from "../ServerEvents";
import { localizedText, translateCurrent as t } from "../i18n";

const names = new Set<keyof Actions>(["history.request-fork", "history.choose-fork", "history.cancel-fork", "history.begin-edit", "history.update-edit", "history.cancel-edit", "history.submit-edit", "question.refresh", "question.answer", "question.cancel", "question.hold", "question.send-offer", "conversation.compact", "conversation.handoff", "conversation.interrupt"]);
export function isApplicationConversationAction(action: string): action is keyof Actions { return names.has(action as keyof Actions); }
function immutable<T>(value: T): ConversationReadonly<T> {
  const clone = structuredClone(value);
  const freeze = (item: unknown): void => { if (item && typeof item === "object") { Object.values(item).forEach(freeze); Object.freeze(item); } };
  freeze(clone); return clone as ConversationReadonly<T>;
}
function updated<T>(update: SetStateAction<T>, current: T): T { return typeof update === "function" ? (update as (value: T) => T)(current) : update; }

/** Internal assembly only. Selected UIs receive immutable views and named actions. */
export interface ApplicationConversationActionsHost {
  getState(): AppState;
  updateState(update: SetStateAction<AppState>): void;
  changed(): void;
  isNavigationPending(): boolean;
  drafts: {
    get(pane?: ConversationPaneID): ComposerDraftState;
    set(draft: ComposerDraftState, pane?: ConversationPaneID): void;
    resolve(imageIds: readonly string[], fileIds: readonly string[]): Promise<{ images: InputImage[]; files: InputFile[] }>;
    /** Admit ready history attachments into the existing application resource owner. */
    importHistory(images: readonly InputImage[], files: readonly InputFile[]): { imageIds: string[]; fileIds: string[] };
  };
  pendingComposer: { threadHasPendingComposerMessages(threadID: string): boolean };
  submission: {
    submitToThread(message: QueuedComposerMessage, thread: Thread, prepareThread?: () => Promise<Thread>): Promise<boolean>;
    steerToThread(message: QueuedComposerMessage, thread: Thread): Promise<boolean>;
    interruptThread(threadId: string): Promise<void>;
  };
}

export class ApplicationConversationActions {
  private pendingFork?: PendingForkState;
  private forkMode?: "local" | "worktree";
  private editing?: HistoryMessageEditState;
  private editDraft?: ConversationEditDraftV1;
  private questions: UserQuestionRequest[] = [];
  private readonly questionPending = new Set<string>();
  private readonly resolvedQuestions = new Set<string>();
  private readonly compacting = new Set<string>();
  private readonly handingOff = new Set<string>();
  private questionError?: string;
  private questionRevision = 0;
  private lifetime = 0;
  private refreshID = 0;
  private stopEvents?: () => void;
  private history;

  constructor(private readonly host: ApplicationConversationActionsHost) {
    const stateRef = { get current() { return host.getState(); }, set current(state: AppState) { host.updateState(state); } };
    const setDraftPart = <K extends keyof ComposerDraftState>(key: K, update: SetStateAction<ComposerDraftState[K]>): void => {
      const draft = host.drafts.get(); host.drafts.set({ ...draft, [key]: updated(update, draft[key]) });
    };
    this.history = createConversationHistoryActions({
      appStateRef: stateRef, setAppState: host.updateState,
      getPendingFork: () => this.pendingFork,
      setPendingFork: update => { this.pendingFork = updated(update, this.pendingFork); host.changed(); },
      setHistoryMessageEdit: update => { this.editing = updated(update, this.editing); if (!this.editing) this.editDraft = undefined; host.changed(); },
      getPrompt: () => host.drafts.get().prompt, getComposerImages: () => host.drafts.get().images,
      getComposerFiles: () => host.drafts.get().files, getComposerSelections: () => host.drafts.get().selections ?? [],
      setPrompt: update => setDraftPart("prompt", update), setComposerImages: update => setDraftPart("images", update),
      setComposerFiles: update => setDraftPart("files", update),
      setComposerSelections: update => setDraftPart("selections", updated(update, host.drafts.get().selections ?? [])),
      getSplitComposerDrafts: () => ({ primary: host.drafts.get("primary"), secondary: host.drafts.get("secondary") }),
      setSplitComposerDrafts: update => { const next = updated(update, { primary: host.drafts.get("primary"), secondary: host.drafts.get("secondary") }); host.drafts.set(next.primary, "primary"); host.drafts.set(next.secondary, "secondary"); },
      restorePrimaryComposerDraft: draft => host.drafts.set(draft),
      threadHasPendingComposerMessages: id => host.pendingComposer.threadHasPendingComposerMessages(id),
      sendComposerMessageToThread: (message, thread, prepare) => host.submission.submitToThread(message, thread, prepare),
      worktreeForkNonGitReason: t("app.worktreeRequiresGit"),
      onError: error => { throw error; },
      // Presentation effects belong to the command consumer, not the domain owner.
      closeConversationSearch() {}, clearEnvironmentDialog() {}, scheduleGitStatusRefresh() {},
      disableConversationAutoFollow() {}, enableConversationAutoFollow() {}, rememberConversationScrollForEdit() {},
      restoreConversationScrollForEdit() {}, jumpToUserMessage() {},
    });
  }

  getSnapshot(): ApplicationConversationActionsViewV1 {
    const state = this.host.getState();
    return immutable({
      editing: this.editing && this.editDraft ? { threadId: this.editing.threadID, turnId: this.editing.turnID, itemId: this.editing.itemID, pane: this.editing.pane, submitting: this.editing.submitting, draft: this.editDraft } : undefined,
      pendingFork: this.pendingFork ? { threadId: this.pendingFork.sourceThread.id, turnId: this.pendingFork.turnID, itemId: this.pendingFork.itemID, busyMode: this.forkMode, worktreeDisabledReason: state.gitStatus?.is_repo === false ? t("app.worktreeRequiresGit") : undefined } : undefined,
      questions: this.questions, pendingQuestionIds: [...this.questionPending], questionError: this.questionError,
      compactingThreadIds: [...this.compacting], handoffThreadIds: [...this.handingOff],
    });
  }

  start(): () => void {
    this.stopEvents?.();
    const lifetime = ++this.lifetime;
    this.stopEvents = subscribeServerEvents(event => this.receive(event));
    void this.refreshQuestions().catch(() => undefined);
    return () => { if (this.lifetime !== lifetime) return; this.lifetime++; this.stopEvents?.(); this.stopEvents = undefined; };
  }
  private receive(event: ServerEvent): void {
    if (event.kind !== "notification") return;
    if (event.message.method === "user-question/requested") {
      const request = (event.message.params as { request?: UserQuestionRequest })?.request;
      if (!request) return;
      this.questionRevision++; this.resolvedQuestions.delete(request.request_id);
      this.questions = [...this.questions.filter(row => row.request_id !== request.request_id), structuredClone(request)]; this.host.changed();
    } else if (event.message.method === "user-question/resolved") {
      const id = (event.message.params as { request_id?: string })?.request_id;
      if (id) this.resolveQuestion(id);
    }
  }
  async refreshQuestions(): Promise<void> {
    const lifetime = this.lifetime, refresh = ++this.refreshID, revision = this.questionRevision;
    try {
      const result = await window.wuu.listUserQuestions();
      if (lifetime !== this.lifetime || refresh !== this.refreshID) return;
      const listed = result.questions.filter(row => !this.resolvedQuestions.has(row.request_id));
      // Events after list dispatch are newer than its snapshot.
      this.questions = revision === this.questionRevision ? listed : [...new Map([...listed, ...this.questions].map(row => [row.request_id, row])).values()];
      this.questionError = undefined; this.host.changed();
    } catch (error) {
      if (lifetime === this.lifetime && refresh === this.refreshID) { this.questionError = error instanceof Error ? error.message : String(error); this.host.changed(); }
      throw error;
    }
  }
  private resolveQuestion(id: string): void { this.questionRevision++; this.resolvedQuestions.add(id); this.questions = this.questions.filter(row => row.request_id !== id); this.host.changed(); }
  private thread(id: string): Thread {
    const state = this.host.getState(), thread = threadForTab(state, id);
    if (!state.activeContext || !thread) throw new Error("Conversation is unavailable");
    if (thread.read_only) throw new Error(t("app.childTaskReadOnly"));
    return thread;
  }
  private idle(thread: Thread): void {
    if (isThreadRunning(thread)) throw new Error(t("history.waitForReply"));
    if (this.host.pendingComposer.threadHasPendingComposerMessages(thread.id)) throw new Error(t("history.handlePendingFirst"));
  }
  private target(input: ConversationHistoryTargetV1, editing = false) {
    const thread = this.thread(input.threadId), item = thread.turns.find(turn => turn.id === input.turnId)?.items.find(item => item.id === input.itemId);
    if (!item || editing && item.type !== "user_message") throw new Error("History message is unavailable");
    return { thread, item };
  }
  private matchingEdit(input: ConversationHistoryTargetV1): void {
    if (!this.editing || this.editing.threadID !== input.threadId || this.editing.turnID !== input.turnId || this.editing.itemID !== input.itemId || this.editing.pane !== input.pane) throw new Error("History edit is no longer selected");
    if (this.editing.submitting) throw new Error("History edit is already submitting");
  }

  async invoke<A extends keyof Actions>(action: A, input: Actions[A]["input"]): Promise<Actions[A]["result"]> {
    return this.dispatch(action, input) as Promise<Actions[A]["result"]>;
  }
  private async dispatch(action: keyof Actions, input: Actions[keyof Actions]["input"]): Promise<unknown> {
    switch (action) {
      case "history.request-fork": {
        if (this.forkMode) throw new Error("Fork is already in progress");
        const request = input as Actions["history.request-fork"]["input"], { thread } = this.target(request);
        this.forkMode = "local";
        try { await this.history.forkThreadFromMessage(thread, request.turnId, request.itemId); return { accepted: true }; }
        finally { this.forkMode = undefined; this.host.changed(); }
      }
      case "history.choose-fork": {
        if (this.forkMode) throw new Error("Fork is already in progress");
        if (!this.pendingFork) return;
        const selected = this.pendingFork;
        this.pendingFork = { ...selected, sourceThread: this.target({ threadId: selected.sourceThread.id, turnId: selected.turnID, itemId: selected.itemID }).thread };
        this.forkMode = (input as Actions["history.choose-fork"]["input"]).mode; this.host.changed();
        try { await this.history.choosePendingFork(this.forkMode); } finally { this.forkMode = undefined; this.host.changed(); }
        return;
      }
      case "history.cancel-fork": if (!this.forkMode) { this.pendingFork = undefined; this.host.changed(); } return;
      case "history.begin-edit": {
        if (this.editing?.submitting) throw new Error("History edit is already submitting");
        const selected = input as Actions["history.begin-edit"]["input"], { thread, item } = this.target(selected, true); this.idle(thread);
        const resources = this.host.drafts.importHistory(item.images ?? [], item.files ?? []);
        this.editDraft = { text: item.text ?? "", ...resources, contentParts: structuredClone(item.content_parts) };
        this.history.startEditingThreadMessageFromHistory(thread, selected.turnId, item, selected.pane);
        return { accepted: Boolean(this.editing) };
      }
      case "history.update-edit": {
        const selected = input as Actions["history.update-edit"]["input"]; this.matchingEdit(selected);
        this.editDraft = structuredClone(selected.draft); this.host.changed(); return;
      }
      case "history.cancel-edit": if (!this.editing?.submitting) this.history.cancelEditingThreadMessage(); return;
      case "history.submit-edit": {
        const selected = input as Actions["history.submit-edit"]["input"]; this.matchingEdit(selected);
        const { thread, item } = this.target(selected, true); this.idle(thread);
        const origin = this.host.getState();
        const draft = this.editDraft!; this.editing = { ...this.editing!, submitting: true }; this.host.changed();
        try {
          const attachments = await this.host.drafts.resolve(draft.imageIds, draft.fileIds);
          const current = this.host.getState();
          if (!sameRuntimeContext(current.activeContext, origin.activeContext) || current.activeSessionTabID !== origin.activeSessionTabID || current.activePane !== origin.activePane) throw new Error("History edit source changed during preparation");
          const latest = this.target(selected, true); this.idle(latest.thread);
          await this.history.submitEditedThreadMessageFromHistory(latest.thread, selected.turnId, item, draft.text, attachments.images, attachments.files, structuredClone(draft.contentParts) as MessageContentPart[] | undefined, selected.pane);
        } finally { if (this.editing) this.editing = { ...this.editing, submitting: false }; this.host.changed(); }
        return;
      }
      case "question.refresh": return this.refreshQuestions();
      case "question.answer": case "question.cancel": case "question.hold": case "question.send-offer": return this.questionAction(action, input as Actions["question.answer"]["input"]);
      case "conversation.compact": return this.compact((input as Actions["conversation.compact"]["input"]).threadId);
      case "conversation.handoff": return this.handoff(input as Actions["conversation.handoff"]["input"]);
      case "conversation.interrupt": { const { threadId } = input as Actions["conversation.interrupt"]["input"]; this.thread(threadId); return this.host.submission.interruptThread(threadId); }
      default: throw new Error(`Unknown conversation action: ${action}`);
    }
  }

  private async questionAction(action: "question.answer" | "question.cancel" | "question.hold" | "question.send-offer", input: { requestId: string; answer?: ConversationQuestionAnswerV1; text?: string }): Promise<unknown> {
    const request = this.questions.find(row => row.request_id === input.requestId);
    if (!request) throw new Error("Question is no longer pending");
    if (this.questionPending.has(request.request_id)) throw new Error("Question action is already in progress");
    this.questionPending.add(request.request_id); this.host.changed();
    try {
      if (action === "question.cancel") { await window.wuu.cancelUserQuestion(request.request_id); this.resolveQuestion(request.request_id); return; }
      if (action === "question.hold") {
        if (!window.wuu.holdUserQuestion) throw new Error("Holding questions is unavailable");
        await window.wuu.holdUserQuestion(request.request_id); this.questionRevision++;
        this.questions = this.questions.map(row => row.request_id === request.request_id ? { ...row, expires_at: undefined } : row); this.host.changed(); return;
      }
      if (action === "question.send-offer" && request.mode !== "offer") throw new Error("Question is not an offer");
      const text = input.text?.trim();
      if (action === "question.send-offer" && !text) return { accepted: false };
      if (action === "question.send-offer" && !request.reply_to_tool) {
        const message = createComposerMessage(text!, [], [])!;
        const accepted = await this.host.submission.steerToThread(message, this.thread(request.thread_id));
        if (accepted) { this.resolveQuestion(request.request_id); await window.wuu.cancelUserQuestion(request.request_id); }
        return { accepted };
      }
      const answer = action === "question.answer" ? structuredClone(input.answer) as UserQuestionAnswer : { answers: [{ id: request.questions[0].id, selected: [], custom: text }] };
      await window.wuu.answerUserQuestion(request.request_id, answer); this.resolveQuestion(request.request_id);
      if (request.mode === "offer" && !request.reply_to_tool) {
        const prompt = request.questions.map(question => { const entry = answer.answers.find(row => row.id === question.id); const parts = [...(entry?.selected ?? [])]; if (entry?.custom?.trim()) parts.push(entry.custom.trim()); return `${question.question}\n${parts.join(", ") || "(no answer)"}`; }).join("\n\n");
        if (!await this.host.submission.steerToThread(createComposerMessage(prompt, [], [])!, this.thread(request.thread_id))) throw new Error("Offer answer could not be sent to its conversation");
      }
      return action === "question.send-offer" ? { accepted: true } : undefined;
    } finally { this.questionPending.delete(request.request_id); this.host.changed(); }
  }

  private async compact(id: string): Promise<void> {
    const thread = this.thread(id); this.idle(thread);
    if (thread.engine_id && thread.engine_id !== "wuu") throw new Error(t("slash.compact.externalEngineUnavailable"));
    if (!this.host.getState().initialized || this.host.isNavigationPending()) throw new Error("Conversation navigation is pending");
    if (this.compacting.has(id)) throw new Error("Compaction is already in progress");
    this.compacting.add(id); this.host.changed();
    const optimistic = createOptimisticCompactTurn(Date.now());
    this.host.updateState(state => updateThreadByID(state, id, thread => upsertTurn(thread, optimistic), { running: true, status: localizedText("app.compactingContext") }));
    try {
      const result = await window.wuu.compactThread(id);
      this.host.updateState(state => updateThreadByID(state, id, thread => replaceOptimisticTurn(thread, optimistic.id, result.turn, upsertTurn), { running: true, status: localizedText("app.compactingContext") }));
    } catch (error) {
      const failed = failOptimisticCompactTurn(optimistic, error instanceof Error ? error.message : String(error), Date.now());
      this.host.updateState(state => updateThreadByID(state, id, thread => replaceOptimisticTurn(thread, optimistic.id, failed, upsertTurn), activeThreadForState(state)?.id === id ? { running: false, status: "ready" } : {}));
      throw error;
    } finally { this.compacting.delete(id); this.host.changed(); }
  }
  private async handoff(input: Actions["conversation.handoff"]["input"]): Promise<{ threadId: string }> {
    const source = this.thread(input.threadId), origin = this.host.getState(), context = origin.activeContext!;
    if (source.engine_id && source.engine_id !== "wuu") throw new Error(t("slash.compact.externalEngineUnavailable"));
    if (this.handingOff.has(source.id)) throw new Error("Handoff is already in progress");
    this.handingOff.add(source.id); this.host.changed();
    try {
      const thread = requireThread(await window.wuu.startThread({ provider: input.provider, model: input.model, effort: input.effort,
        handoff: { request_id: crypto.randomUUID(), revision: 1, parent_session_id: source.id, intent: input.intent } }), "thread/start did not return a handoff session");
      this.host.updateState(current => {
        const discoverable = { ...current, sessionTabs: ensureSessionTab(current.sessionTabs, createThreadSessionTab(thread, context)), threads: sameRuntimeContext(current.activeContext, context) ? upsertThread(current.threads, thread) : current.threads };
        if (!sameRuntimeContext(current.activeContext, context) || current.activeSessionTabID !== origin.activeSessionTabID || current.thread?.id !== origin.thread?.id || current.secondaryThread?.id !== origin.secondaryThread?.id || current.activePane !== origin.activePane) return discoverable;
        return { ...setThreadForPane(discoverable, "primary", thread), activePane: "primary", allowThreadAutoActivation: true, activeSessionTabID: threadSessionTabID(thread.id) };
      });
      return { threadId: thread.id };
    } finally { this.handingOff.delete(source.id); this.host.changed(); }
  }
}
