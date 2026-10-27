import type { ApplicationConversationDetailsActionMapV1 as Actions, ApplicationConversationDetailV1 } from "../../../../packages/plugin-sdk/src/ui-conversation";
import type { AppState } from "../AppState";
import type { Thread } from "../../shared/protocol";
import { desktopApiErrorMessage } from "../WorkspaceReviewHelpers";
import { translateCurrent as t } from "../i18n";

export function isApplicationConversationDetailsAction(action: string): action is keyof Actions {
  return action === "conversation.open-context" || action === "conversation.open-instructions" || action === "conversation.dismiss-detail";
}

/** Read diagnostics for the captured conversation; card presentation belongs to UI. */
export class ApplicationConversationDetails {
  private entries: readonly ApplicationConversationDetailV1[] = Object.freeze([]);
  private lifetime = 0;
  constructor(private readonly host: { getState(): AppState; getThread(id: string): Thread | undefined; changed(): void }) {}
  getSnapshot = (): readonly ApplicationConversationDetailV1[] => this.entries;
  stop(): void { this.lifetime++; this.entries = Object.freeze(this.entries.filter(entry => !entry.loading)); }
  private source(): string { const state = this.host.getState(); return JSON.stringify([state.activeContext, state.runtimeConnectionGeneration ?? 0]); }
  private dismiss(id: string): void {
    const next = this.entries.filter(entry => entry.id !== id);
    if (next.length === this.entries.length) return;
    this.entries = Object.freeze(next); this.host.changed();
  }
  async invoke<A extends keyof Actions>(action: A, input: Actions[A]["input"], signal?: AbortSignal): Promise<Actions[A]["result"]> {
    signal?.throwIfAborted();
    if (action === "conversation.dismiss-detail") { this.dismiss((input as Actions["conversation.dismiss-detail"]["input"]).entryId); return undefined as Actions[A]["result"]; }
    const { threadId } = input as Actions["conversation.open-context"]["input"];
    const thread = this.host.getThread(threadId);
    if (!thread || !this.host.getState().activeContext) throw new Error(t("contextComposition.noCurrentConversation"));
    const source = this.source(), lifetime = this.lifetime;
    const id = `detail:${crypto.randomUUID()}`;
    const kind = action === "conversation.open-context" ? "context" : "instructions";
    const entry: ApplicationConversationDetailV1 = Object.freeze({ id, kind, threadID: thread.id,
      title: thread.preview || thread.title, afterTurnID: kind === "context" ? thread.turns.at(-1)?.id : undefined, loading: true });
    this.entries = Object.freeze([...this.entries, entry]); this.host.changed();
    const abort = () => this.dismiss(id);
    signal?.addEventListener("abort", abort, { once: true });
    const publish = (next: ApplicationConversationDetailV1): void => {
      if (lifetime !== this.lifetime || source !== this.source()) { this.dismiss(id); return; }
      if (!this.entries.some(entry => entry.id === id)) return;
      const copy = structuredClone(next);
      const freeze = (value: unknown): void => { if (value && typeof value === "object") { Object.values(value).forEach(freeze); Object.freeze(value); } };
      freeze(copy);
      this.entries = Object.freeze(this.entries.map(entry => entry.id === id ? copy : entry)); this.host.changed();
    };
    try {
      if (kind === "context") {
        const result = await window.wuu.getThreadContextComposition(thread.id);
        signal?.throwIfAborted(); publish({ ...entry, kind, loading: false, result });
      } else {
        const result = await window.wuu.listInstructionFiles();
        signal?.throwIfAborted(); publish({ ...entry, kind, loading: false, result });
      }
    } catch (error) {
      signal?.throwIfAborted();
      publish({ ...entry, loading: false, error: desktopApiErrorMessage(error, t(kind === "context" ? "contextComposition.readFailed" : "instructions.readFailed")) });
    } finally { signal?.removeEventListener("abort", abort); }
    return { entryId: id } as Actions[A]["result"];
  }
}
