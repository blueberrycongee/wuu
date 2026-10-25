import type { SetStateAction } from "react";
import type { ApplicationSearchActionMapV1 as Actions, ApplicationSearchResultV1, ApplicationSearchTargetV1, ApplicationSearchViewV1 } from "../../../../packages/plugin-sdk/src/ui-search";
import type { RuntimeContext, Thread, ThreadSearchResultItem } from "../../shared/protocol";
import { activeThreadForState, sameRuntimeContext, summarizeThreadForSidebar, updateThreadByID, updateTurnItem, type AppState } from "../AppState";
import { conversationSearchPattern } from "../ConversationSearchDisplay";

const resultLimit = 100;
const names = new Set<keyof Actions>(["search.query", "search.run", "search.locate", "search.page", "search.cancel", "search.complete-target"]);
export function isApplicationSearchAction(action: string): action is keyof Actions { return names.has(action as keyof Actions); }
function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => { if (item && typeof item === "object") { Object.values(item).forEach(freeze); Object.freeze(item); } };
  freeze(copy); return copy;
}

/** All dependencies are internal controller owners, never selected-UI callbacks. */
export interface ApplicationSearchHost {
  getState(): AppState;
  updateState(update: SetStateAction<AppState>): void;
  subscribeState(listener: () => void): () => void;
  changed(): void;
  cacheThreads(threads: Thread[]): void;
  /** Claims the shared navigation synchronously, then waits for activation. */
  activateThread(threadId: string, signal: AbortSignal): Promise<void>;
  getNavigation(): { revision: number; pending: boolean };
}
type TargetWork = {
  view: ApplicationSearchTargetV1;
  abort: AbortController;
  revision?: number;
  context?: RuntimeContext;
  activated: boolean;
  loading: boolean;
  lastCursor?: string;
};

/** Owns query/locate work across shell replacement, using existing thread state. */
export class ApplicationSearch {
  private query = "";
  private composing = false;
  private loading = false;
  private error?: string;
  private results: ApplicationSearchResultV1[] = [];
  private accepted: { id: string; result: ThreadSearchResultItem }[] = [];
  private request = 0;
  private acceptedRequest = -1;
  private queryContext?: RuntimeContext;
  private timer?: ReturnType<typeof setTimeout>;
  private work?: TargetWork;
  private stopState?: () => void;
  private lifetime = 0;
  private reconciling = false;
  constructor(private readonly host: ApplicationSearchHost) {}

  getSnapshot(): ApplicationSearchViewV1 {
    return immutable({ query: this.query, composing: this.composing, loading: this.loading, error: this.error, results: this.results, target: this.work?.view });
  }
  start(): () => void {
    this.stopState?.();
    const lifetime = ++this.lifetime;
    this.stopState = this.host.subscribeState(() => this.reconcile());
    this.reconcile();
    return () => {
      if (lifetime !== this.lifetime) return;
      this.lifetime++; this.stopState?.(); this.stopState = undefined;
      this.invalidateQuery(); this.clearTarget();
    };
  }
  private invalidateQuery(): void {
    this.request++;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined; this.loading = false;
  }
  private setQuery(query: string, composing: boolean): void {
    if (query === this.query && composing === this.composing) return;
    this.invalidateQuery(); this.query = query; this.composing = composing; this.error = undefined;
    this.queryContext = this.host.getState().activeContext;
    this.loading = Boolean(this.queryContext);
    // Results remain visible during debounce, but their IDs are no longer valid.
    if (this.queryContext && !composing) this.timer = setTimeout(() => { this.timer = undefined; void this.run(); }, query.trim() ? 140 : 0);
    this.host.changed();
  }
  private async run(): Promise<void> {
    this.invalidateQuery();
    const source = this.host.getState().activeContext;
    this.queryContext = source;
    if (!source || this.composing) { this.host.changed(); return; }
    const request = this.request, query = this.query;
    this.loading = true; this.error = undefined; this.host.changed();
    const current = () => request === this.request && sameRuntimeContext(source, this.host.getState().activeContext) && source.cwd === this.host.getState().activeContext?.cwd;
    try {
      const result = await window.wuu.searchThreads(query, resultLimit);
      if (!current()) return;
      const rows = structuredClone(result.results);
      this.host.cacheThreads(rows.map(row => row.thread));
      if (!current()) return;
      this.acceptedRequest = request;
      this.accepted = rows.map((result, index) => ({ id: `${request}:${index}`, result }));
      this.results = this.accepted.map(({ id, result }) => ({ resultId: id, thread: summarizeThreadForSidebar(result.thread), snippet: result.snippet, messageSeq: result.message_seq }));
      this.loading = false; this.host.changed();
    } catch (error) {
      if (!current()) return;
      this.error = error instanceof Error ? error.message : String(error); this.loading = false; this.host.changed();
    }
  }
  private clearTarget(id?: string): void {
    const work = this.work;
    if (!work || id !== undefined && work.view.id !== id) return;
    this.work = undefined;
    // Once activation succeeds, completion/cancel must not undo its runtime switch.
    if (!work.activated) work.abort.abort();
    this.host.updateState(state => state.conversationSearchTarget?.id === work.view.id ? { ...state, conversationSearchTarget: undefined } : state);
    this.host.changed();
  }
  private owns(work: TargetWork): boolean {
    if (this.work !== work || work.abort.signal.aborted) return false;
    const navigation = this.host.getNavigation();
    return work.revision === navigation.revision && activeThreadForState(this.host.getState())?.id === work.view.threadId
      && sameRuntimeContext(work.context, this.host.getState().activeContext) && work.context?.cwd === this.host.getState().activeContext?.cwd;
  }
  private setPhase(work: TargetWork, phase: ApplicationSearchTargetV1["phase"], details: Pick<ApplicationSearchTargetV1, "itemId" | "turnId" | "error"> = {}): void {
    if (this.work !== work) return;
    work.view = { ...work.view, phase, itemId: undefined, turnId: undefined, error: undefined, ...details };
    this.host.changed();
  }
  private fail(work: TargetWork, error: unknown): void {
    if (!this.owns(work)) return;
    this.setPhase(work, "failed", { error: error instanceof Error ? error.message : String(error) });
  }
  private async locate(resultId: string): Promise<{ targetId?: string }> {
    const selected = this.accepted.find(row => row.id === resultId);
    if (!selected || this.acceptedRequest !== this.request || !sameRuntimeContext(this.queryContext, this.host.getState().activeContext) || this.queryContext?.cwd !== this.host.getState().activeContext?.cwd) throw new Error("Search result is no longer current");
    this.clearTarget();
    const { result } = selected;
    const id = crypto.randomUUID();
    const work: TargetWork = { view: { id, threadId: result.thread.id, messageSeq: result.message_seq ?? 0, query: this.query, phase: "activating" }, abort: new AbortController(), activated: false, loading: false };
    this.work = work;
    this.host.updateState(state => ({ ...state, conversationSearchTarget: result.message_seq !== undefined ? { id, result, query: this.query, ready: false } : undefined }));
    this.host.changed();
    try {
      const activation = this.host.activateThread(result.thread.id, work.abort.signal);
      work.revision = this.host.getNavigation().revision;
      await activation;
      if (this.work !== work || work.abort.signal.aborted) return {};
      work.context = this.host.getState().activeContext;
      if (!this.owns(work)) { this.clearTarget(id); return {}; }
      work.activated = true;
      if (result.message_seq === undefined) { this.clearTarget(id); return {}; }
      this.host.updateState(state => state.conversationSearchTarget?.id === id ? { ...state, conversationSearchTarget: { ...state.conversationSearchTarget, ready: true } } : state);
      this.reconcile();
      return { targetId: id };
    } catch (error) {
      if (this.work !== work || work.abort.signal.aborted) return {};
      // Failed activation has no destination context to satisfy owns().
      this.setPhase(work, "failed", { error: error instanceof Error ? error.message : String(error) });
      throw error;
    }
  }
  private reconcile(): void {
    if (this.reconciling) return;
    this.reconciling = true;
    try {
      if (this.queryContext && (!sameRuntimeContext(this.queryContext, this.host.getState().activeContext) || this.queryContext.cwd !== this.host.getState().activeContext?.cwd)) {
        this.invalidateQuery(); this.queryContext = undefined; this.accepted = []; this.results = []; this.error = undefined; this.host.changed();
      }
      const work = this.work;
      if (!work) return;
      if (work.revision !== undefined && this.host.getNavigation().revision !== work.revision) { this.clearTarget(work.view.id); return; }
      if (!work.activated) return;
      if (!this.owns(work)) { this.clearTarget(work.view.id); return; }
      if (this.host.getNavigation().pending || work.loading || ["ready", "not-found", "failed"].includes(work.view.phase)) return;
      const thread = activeThreadForState(this.host.getState())!;
      const seq = work.view.messageSeq;
      const turn = thread.turns.find(turn => turn.items.some(item => item.seq === seq && (item.type === "user_message" || item.type === "agent_message")));
      if (!turn) {
        const passed = thread.turns.some(turn => turn.items.some(item => item.seq !== undefined && item.seq < seq));
        const cursor = passed ? undefined : thread.history_cursor;
        if (!cursor) { this.setPhase(work, "not-found"); return; }
        if (!window.wuu.loadEarlierThreadHistory) { this.fail(work, new Error("Earlier conversation history is unavailable")); return; }
        this.setPhase(work, "paging");
        if (cursor === work.lastCursor) return;
        work.lastCursor = cursor; work.loading = true;
        void window.wuu.loadEarlierThreadHistory(thread.id, cursor).catch(error => this.fail(work, error)).finally(() => { work.loading = false; if (this.work === work) this.reconcile(); });
        return;
      }
      const items = turn.items.filter(item => item.seq === seq && (item.type === "user_message" || item.type === "agent_message"));
      const preview = items.find(item => item.remote_content_ref);
      if (preview?.remote_content_ref) {
        if (!window.wuu.readRemoteItem) { this.fail(work, new Error("Conversation message content is unavailable")); return; }
        const ref = preview.remote_content_ref;
        work.loading = true; this.setPhase(work, "hydrating");
        void window.wuu.readRemoteItem(ref).then(complete => {
          if (!this.owns(work)) return;
          if (complete.id !== preview.id || complete.type !== preview.type || complete.remote_content_ref) throw new Error("Content changed during download");
          this.host.updateState(state => updateThreadByID(state, thread.id, current => updateTurnItem(current, turn.id, preview.id, item => item.remote_content_ref === ref ? complete : item)));
        }).catch(error => this.fail(work, error)).finally(() => { work.loading = false; if (this.work === work) this.reconcile(); });
        return;
      }
      const item = items.find(item => conversationSearchPattern(work.view.query)?.test(item.text ?? ""));
      if (!item) { this.setPhase(work, "not-found"); return; }
      this.setPhase(work, "ready", { turnId: turn.id, itemId: item.id });
    } finally { this.reconciling = false; }
  }
  async invoke<A extends keyof Actions>(action: A, input: Actions[A]["input"]): Promise<Actions[A]["result"]> {
    switch (action) {
      case "search.query": { const request = input as Actions["search.query"]["input"]; this.setQuery(request.query, request.composing ?? false); break; }
      case "search.run": { const request = input as Actions["search.run"]["input"]; if (request.query !== undefined) this.query = request.query; this.composing = false; await this.run(); break; }
      case "search.locate": return await this.locate((input as Actions["search.locate"]["input"]).resultId) as Actions[A]["result"];
      case "search.page": {
        const { targetId } = input as Actions["search.page"]["input"];
        const work = this.work;
        if (!work || work.view.id !== targetId) throw new Error("Search target is no longer current");
        if (work.view.phase === "failed" && work.activated && !work.loading) { work.lastCursor = undefined; this.setPhase(work, "paging"); }
        this.reconcile(); break;
      }
      case "search.cancel": { const { targetId } = input as Actions["search.cancel"]["input"]; if (!targetId) { this.invalidateQuery(); this.error = undefined; } this.clearTarget(targetId); this.host.changed(); break; }
      case "search.complete-target": this.clearTarget((input as Actions["search.complete-target"]["input"]).targetId); break;
      default: throw new Error(`Unknown search action: ${action}`);
    }
    return undefined as Actions[A]["result"];
  }
}
