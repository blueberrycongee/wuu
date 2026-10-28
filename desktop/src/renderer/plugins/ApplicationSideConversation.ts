import type { SetStateAction } from "react";
import type { ApplicationSideConversationActionMapV1 as Actions, ApplicationSideConversationViewV1 } from "../../../../packages/plugin-sdk/src/ui-side-conversation";
import type { RuntimeContext, SideThreadSelection, Thread, ThreadStartParams } from "../../shared/protocol";
import { formatPageContext } from "../../shared/pageContext";
import { activeThreadForState, sameRuntimeContext, requireThread, threadForTab, upsertThread, type AppState } from "../AppState";
import { SideThreadOwner } from "../SideThreadController";
import { sideThreadMessagesToTurns } from "../SideThreadTurns";
import { buildComposerSlashCommands } from "../ComposerSlashCommands";
import { createComposerMessage, type QueuedComposerMessage } from "../ComposerMessages";
import { hasAuthenticatedProvider } from "../RuntimeHelpers";
import { translateCurrent as t } from "../i18n";
import { createImmutableSnapshotProjector } from "./ImmutableSnapshot";

const names = new Set<keyof Actions>(["side.open", "side.close", "side.set-draft", "side.set-selection", "side.send", "side.interrupt", "side.reset", "context-chat.submit"]);
export function isApplicationSideConversationAction(action: string): action is keyof Actions { return names.has(action as keyof Actions); }
export interface ApplicationSideConversationHost {
  getState(): AppState;
  updateState(update: SetStateAction<AppState>): void;
  subscribeState(listener: () => void): () => void;
  changed(): void;
  isNavigationPending(): boolean;
  activateThread(id: string, signal: AbortSignal): Promise<void>;
  openPanel(): void;
  closePanel(): void;
  getContextChatRuntime(): { pending: boolean; requiresHostModel: boolean; params: ThreadStartParams };
  submitBackground(message: QueuedComposerMessage, thread: Thread, context: RuntimeContext): Promise<boolean>;
  cacheThread(thread: Thread, context: RuntimeContext): void;
}

/** One existing side reducer owner; ordinary Context Chat stays in shared thread/submission state. */
export class ApplicationSideConversation {
  private readonly projectSnapshot = createImmutableSnapshotProjector();
  private readonly side: SideThreadOwner;
  private readonly selectedCommands = new Map<string, string>();
  private readonly pendingContext = new Map<string, { sourceId: string; sessionId?: string }>();
  private selectionAbort?: AbortController;
  private syncing = false;
  constructor(private readonly host: ApplicationSideConversationHost) { this.side = new SideThreadOwner(this.options()); }
  private commands() {
    const state = this.host.getState();
    return buildComposerSlashCommands({ activeContext: state.activeContext, initialized: state.initialized, running: false }).filter(command => command.action === "open-side-thread");
  }
  private options() {
    const state = this.host.getState(), activeThreadId = activeThreadForState(state)?.id;
    const commands = this.commands(), selected = this.selectedCommands.get(`${state.activeContext?.cwd ?? ""}\0${activeThreadId ?? ""}`);
    const command = commands.find(command => !selected || `${command.pluginId}.${command.pluginCommandId}` === selected);
    return { activeThreadId, activeContext: state.activeContext, commandID: command ? `${command.pluginId}.${command.pluginCommandId}` : undefined, disabled: !command };
  }
  private sync(): void {
    if (this.syncing) return;
    this.syncing = true;
    try {
      const options = this.options(); this.side.configure(options);
      if (options.disabled && options.activeThreadId && this.side.getSnapshot().byThread[options.activeThreadId]?.open) { this.side.close(); this.host.closePanel(); }
    } finally { this.syncing = false; }
  }
  start(): () => void {
    this.sync();
    const stopState = this.host.subscribeState(() => this.sync()), stopSide = this.side.subscribe(() => this.host.changed()), stopOwner = this.side.start();
    return () => { this.selectionAbort?.abort(); stopState(); stopSide(); stopOwner(); };
  }
  getSnapshot(): ApplicationSideConversationViewV1 {
    const options = this.options(), entry = options.activeThreadId ? this.side.getSnapshot().byThread[options.activeThreadId] : undefined;
    return this.projectSnapshot({ parentThreadId: options.activeThreadId, commandId: options.commandID, commands: this.commands().map(command => ({ id: `${command.pluginId}.${command.pluginCommandId}`, title: command.title, description: command.description })), entry,
      turns: sideThreadMessagesToTurns(entry?.messages ?? []), disabledReason: this.side.sendDisabledReason, contextChatPending: [...this.pendingContext.values()] });
  }
  private current(parentThreadId: string): void {
    this.sync();
    if (activeThreadForState(this.host.getState())?.id !== parentThreadId || this.host.isNavigationPending()) throw new Error("The side conversation source changed");
  }
  private choose(commandId?: string): string {
    const options = this.options(), chosen = commandId ?? options.commandID;
    if (!chosen || !this.commands().some(command => `${command.pluginId}.${command.pluginCommandId}` === chosen)) throw new Error(t("sideThread.unavailable"));
    this.selectedCommands.set(`${options.activeContext?.cwd ?? ""}\0${options.activeThreadId ?? ""}`, chosen); this.sync(); this.host.changed(); return chosen;
  }
  private async open(input: Actions["side.open"]["input"]): Promise<void> {
    const selection = structuredClone(input.selection) as SideThreadSelection | undefined;
    if (selection?.type === "response" && selection.response.source.thread_id !== input.parentThreadId) throw new Error("Selected response belongs to another conversation");
    this.selectionAbort?.abort(); const abort = new AbortController(); this.selectionAbort = abort;
    if (activeThreadForState(this.host.getState())?.id !== input.parentThreadId) await this.host.activateThread(input.parentThreadId, abort.signal);
    abort.signal.throwIfAborted(); this.current(input.parentThreadId);
    const command = this.choose(input.commandId); this.host.openPanel();
    if (selection) this.side.setDraftSelection(selection);
    await this.side.open();
    if (!abort.signal.aborted && input.prompt?.trim()) { this.current(input.parentThreadId); await this.side.sendMessage(input.prompt, command); }
  }
  private async contextChat(input: Actions["context-chat.submit"]["input"]): Promise<{ sessionId: string; sent: boolean }> {
    // Snapshot once at admission; neither current page nor active draft is consulted later.
    const source = structuredClone(input), context = source.context as RuntimeContext, state = this.host.getState();
    if (!source.text.trim() || !state.initialized || this.host.isNavigationPending() || !sameRuntimeContext(context, state.activeContext) || context.cwd !== state.activeContext?.cwd) throw new Error("The workspace changed. Return to the source workspace and send again.");
    const key = JSON.stringify([context, source.page.kind, source.page.pageId]);
    if (this.pendingContext.has(key)) throw new Error("This page conversation is already submitting");
    const reference = formatPageContext(source.page), message = createComposerMessage(source.text + reference, [], [], [{ type: "text", text: source.text }, { type: "text", text: reference }])!;
    let thread = source.sessionId ? threadForTab(state, source.sessionId) : undefined;
    if (source.sessionId && (!thread || thread.read_only || thread.archived)) throw new Error("This conversation is no longer available. Open Chat to continue it.");
    this.pendingContext.set(key, { sourceId: source.page.pageId, sessionId: source.sessionId }); this.host.changed();
    try {
      if (!thread) {
        const runtime = this.host.getContextChatRuntime(), params = structuredClone(runtime.params);
        if (runtime.pending) throw new Error("The selected agent is still loading. Try again shortly.");
        if (runtime.requiresHostModel && !hasAuthenticatedProvider(state.initialized.providers)) throw new Error("Configure a model before sending.");
        this.host.updateState(current => ({ ...current, allowThreadAutoActivation: false }));
        thread = requireThread(await window.wuu.startThread(params, context), "thread/start did not return a thread");
        const created = thread;
        this.host.cacheThread(created, context);
        this.host.updateState(current => sameRuntimeContext(current.activeContext, context) && current.activeContext?.cwd === context.cwd ? { ...current, threads: upsertThread(current.threads, created) } : current);
        this.pendingContext.set(key, { sourceId: source.page.pageId, sessionId: created.id }); this.host.changed();
      }
      return { sessionId: thread.id, sent: await this.host.submitBackground(message, thread, context) };
    } finally { this.pendingContext.delete(key); this.host.changed(); }
  }
  async invoke<A extends keyof Actions>(action: A, input: Actions[A]["input"]): Promise<Actions[A]["result"]> {
    if (action === "context-chat.submit") return await this.contextChat(input as Actions["context-chat.submit"]["input"]) as Actions[A]["result"];
    if (action === "side.open") { await this.open(input as Actions["side.open"]["input"]); return undefined as Actions[A]["result"]; }
    const request = input as Exclude<Actions[keyof Actions]["input"], Actions["context-chat.submit"]["input"]>;
    this.current(request.parentThreadId);
    switch (action) {
      case "side.close": this.side.close(); this.host.closePanel(); break;
      case "side.set-draft": this.side.setDraft((input as Actions["side.set-draft"]["input"]).text); break;
      case "side.set-selection": {
        const selection = structuredClone((input as Actions["side.set-selection"]["input"]).selection) as SideThreadSelection | undefined;
        if (selection?.type === "response" && selection.response.source.thread_id !== request.parentThreadId) throw new Error("Selected response belongs to another conversation");
        this.side.setDraftSelection(selection); break;
      }
      case "side.send": {
        const send = input as Actions["side.send"]["input"], command = this.choose(send.commandId);
        return { accepted: await this.side.sendMessage(send.text ?? this.side.getSnapshot().byThread[send.parentThreadId]?.draft ?? "", command) } as Actions[A]["result"];
      }
      case "side.interrupt": await this.side.interrupt(); break;
      case "side.reset": await this.side.reset(); break;
      default: throw new Error(`Unknown side conversation action: ${action}`);
    }
    return undefined as Actions[A]["result"];
  }
}
