import type { SetStateAction } from "react";
import type { ApplicationSideConversationActionMapV1 as Actions, ApplicationSideConversationViewV1 } from "../../../../packages/plugin-sdk/src/ui-side-conversation";
import type { RuntimeContext, Thread, ThreadStartParams } from "../../shared/protocol";
import { formatPageContext } from "../../shared/pageContext";
import { activeThreadForState, sameRuntimeContext, requireThread, threadForTab, upsertThread, type AppState } from "../AppState";
import { createComposerMessage, type QueuedComposerMessage } from "../ComposerMessages";
import { hasAuthenticatedProvider } from "../RuntimeHelpers";
import { createImmutableSnapshotProjector } from "./ImmutableSnapshot";

const names = new Set<keyof Actions>(["side.open", "side.list", "side.promote", "side.delete", "context-chat.submit"]);
export function isApplicationSideConversationAction(action: string): action is keyof Actions { return names.has(action as keyof Actions); }

interface Host {
  getState(): AppState;
  updateState(update: SetStateAction<AppState>): void;
  changed(): void;
  isNavigationPending(): boolean;
  open(input: Actions["side.open"]["input"], signal?: AbortSignal): Promise<Actions["side.open"]["result"]>;
  remove(threadId: string, promote: boolean): Promise<void>;
  getContextChatRuntime(): { pending: boolean; requiresHostModel: boolean; params: ThreadStartParams };
  submitBackground(message: QueuedComposerMessage, thread: Thread, context: RuntimeContext): Promise<boolean>;
  cacheThread(thread: Thread, context: RuntimeContext): void;
}

export class ApplicationSideConversation {
  private catalogRevision = 0;
  private readonly projectSnapshot = createImmutableSnapshotProjector();
  private readonly pendingContext = new Map<string, { sourceId: string; sessionId?: string }>();
  constructor(private readonly host: Host) {}

  invalidateCatalog(): void {
    this.catalogRevision++;
    this.host.changed();
  }

  getSnapshot(): ApplicationSideConversationViewV1 {
    const thread = activeThreadForState(this.host.getState());
    return this.projectSnapshot({ parentThreadId: thread?.placement || thread?.parent_id || thread?.ephemeral || thread?.read_only ? undefined : thread?.id,
      catalogRevision: this.catalogRevision, contextChatPending: [...this.pendingContext.values()] });
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
  async invoke<A extends keyof Actions>(action: A, input: Actions[A]["input"], signal?: AbortSignal): Promise<Actions[A]["result"]> {
    if (action === "context-chat.submit") return await this.contextChat(input as Actions["context-chat.submit"]["input"]) as Actions[A]["result"];
    if (action === "side.open") return await this.host.open(input as Actions["side.open"]["input"], signal) as Actions[A]["result"];
    if (action === "side.list") {
      const parentThreadId = (input as Actions["side.list"]["input"]).parentThreadId;
      if (this.getSnapshot().parentThreadId !== parentThreadId) throw new Error("The side conversation source changed");
      return await window.wuu.listThreads(undefined, { parentThreadId }) as Actions[A]["result"];
    }
    await this.host.remove((input as Actions["side.delete"]["input"]).threadId, action === "side.promote");
    return undefined as Actions[A]["result"];
  }
}
