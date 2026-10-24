import { useEffect, useRef, useSyncExternalStore } from "react";
import type { RuntimeContext, SideThreadEventEnvelope, SideThreadHistoryResult, SideThreadOpenResult, SideThreadSendParams, SideThreadSendResult, SideThreadSelection } from "../shared/protocol";
import { createInitialSideThreadStore, ensureSideThreadEntry, reduceSideThreadStore, sideThreadRequestError, type SideThreadAction, type SideThreadStoreState } from "./SideThreadState";
import { translateCurrent as t, useI18n } from "./i18n";
import { statusMessageForError } from "./UserFacingErrors";
import { showActionErrorToast } from "./Toast";

const SIDE_THREAD_RECOVERY_POLL_MS = 2_000;

export type SideThreadController = {
  entry: SideThreadStoreState["byThread"][string] | undefined;
  open: () => void;
  close: () => void;
  toggle: () => void;
  setDraft: (draft: string) => void;
  setDraftSelection: (selection: SideThreadSelection | undefined) => void;
  sendMessage: (prompt: string, commandID?: string) => void;
  interrupt: () => void;
  reset: () => void;
  sendDisabledReason?: string;
};

export type SideThreadControllerOptions = {
  activeThreadId: string | undefined;
  activeContext?: RuntimeContext;
  ipc?: SideThreadIPC;
  commandID?: string;
  disabled?: boolean;
  disabledReason?: string;
};

export type SideThreadIPC = {
  openSideThread: (mainThreadId: string) => Promise<SideThreadOpenResult>;
  getSideThreadHistory: (
    mainThreadId: string
  ) => Promise<SideThreadHistoryResult | null>;
  sendSideThreadMessage: (
    params: SideThreadSendParams
  ) => Promise<SideThreadSendResult>;
  interruptSideThread: (mainThreadId: string) => Promise<{ ok: boolean }>;
  resetSideThread: (mainThreadId: string) => Promise<{ ok: boolean }>;
  onSideThreadEvent: (
    handler: (envelope: SideThreadEventEnvelope) => void
  ) => () => void;
};

function defaultIPC(): SideThreadIPC | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }
  const candidate = (window as unknown as { wuu?: Partial<SideThreadIPC> }).wuu;
  if (
    typeof candidate?.openSideThread !== "function" ||
    typeof candidate.getSideThreadHistory !== "function" ||
    typeof candidate.sendSideThreadMessage !== "function" ||
    typeof candidate.interruptSideThread !== "function" ||
    typeof candidate.resetSideThread !== "function" ||
    typeof candidate.onSideThreadEvent !== "function"
  ) {
    return undefined;
  }
  return candidate as SideThreadIPC;
}

function errorMessage(error: unknown): string { return error instanceof Error ? error.message : String(error); }

/** The same reducer and async owner serve both the legacy hook and public host. */
export class SideThreadOwner {
  private store = createInitialSideThreadStore();
  private readonly listeners = new Set<() => void>();
  private readonly announced = new Map<string, string>();
  private readonly generations = new Map<string, number>();
  private readonly resets = new Map<string, number>();
  private readonly sends = new Map<string, Promise<boolean>>();
  private stopEvents?: () => void;
  private timer?: ReturnType<typeof setInterval>;
  private polling = false;
  private lifetime = 0;
  private started = false;
  constructor(private options: SideThreadControllerOptions) {
    if (options.activeThreadId) this.store = ensureSideThreadEntry(this.store, options.activeThreadId).store;
  }
  getSnapshot = (): SideThreadStoreState => this.store;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private ipc(): SideThreadIPC | undefined { return this.options.ipc ?? defaultIPC(); }
  private key(): string | undefined { return this.options.activeThreadId && this.options.activeContext?.cwd ? `${this.options.activeContext.cwd}\0${this.options.activeThreadId}` : undefined; }
  get sendDisabledReason(): string | undefined {
    return !this.ipc() ? t("sideThread.unsupported") : this.options.disabled ? this.options.disabledReason ?? t("sideThread.unavailable") : !this.options.activeContext ? t("sideThread.selectWorkspace") : undefined;
  }
  private announce(): void {
    const id = this.options.activeThreadId, entry = id ? this.store.byThread[id] : undefined;
    if (!id || !entry) return;
    const error = sideThreadRequestError(entry);
    if (!error || this.announced.get(id) === error) return;
    this.announced.set(id, error); showActionErrorToast(statusMessageForError(error, t("sideThread.unavailable")));
  }
  private dispatch(action: SideThreadAction): void {
    const next = reduceSideThreadStore(this.store, action);
    if (next === this.store) return;
    this.store = next; this.listeners.forEach(listener => listener()); this.announce();
  }
  configure(options: SideThreadControllerOptions): void {
    const previous = this.key(), previousIPC = this.ipc(), wasDisabled = this.options.disabled;
    this.options = options;
    if (options.activeThreadId) {
      const next = ensureSideThreadEntry(this.store, options.activeThreadId).store;
      if (next !== this.store) { this.store = next; this.listeners.forEach(listener => listener()); }
    }
    if (this.started && previousIPC !== this.ipc()) this.bindEvents();
    if (this.started && (previous !== this.key() || wasDisabled && !options.disabled) && options.activeThreadId && this.store.byThread[options.activeThreadId]?.open && !this.sendDisabledReason) void this.refresh(false);
    this.announce();
  }
  private bindEvents(): void {
    this.stopEvents?.();
    this.stopEvents = this.ipc()?.onSideThreadEvent(envelope => {
      if (envelope.workdir !== this.options.activeContext?.cwd) return;
      if (envelope.event.type === "reset") this.invalidateReset(envelope.event.main_thread_id, envelope.workdir);
      this.dispatch({ type: "applyEvent", event: envelope.event });
    });
  }
  start(): () => void {
    if (this.timer !== undefined) clearInterval(this.timer);
    this.started = true; const lifetime = ++this.lifetime; this.bindEvents();
    this.timer = setInterval(() => { void this.poll(); }, SIDE_THREAD_RECOVERY_POLL_MS);
    return () => {
      if (lifetime !== this.lifetime) return;
      this.started = false; this.lifetime++; this.stopEvents?.(); this.stopEvents = undefined;
      if (this.timer !== undefined) clearInterval(this.timer); this.timer = undefined;
    };
  }
  private invalidateReset(id: string, cwd: string): void {
    const key = `${cwd}\0${id}`;
    this.resets.set(key, (this.resets.get(key) ?? 0) + 1);
    this.generations.set(key, (this.generations.get(key) ?? 0) + 1);
  }
  private async refresh(explicit: boolean): Promise<void> {
    const id = this.options.activeThreadId, key = this.key(), ipc = this.ipc(), lifetime = this.lifetime;
    if (!id || !key || !ipc || this.sendDisabledReason) return;
    const generation = (this.generations.get(key) ?? 0) + 1; this.generations.set(key, generation);
    const current = () => this.lifetime === lifetime && !this.sendDisabledReason && this.key() === key && this.generations.get(key) === generation;
    try {
      const opened = await ipc.openSideThread(id);
      if (!current() || !opened.summary) return;
      this.dispatch({ type: "mergeSummary", mainThreadId: id, summary: opened.summary });
      const history = await ipc.getSideThreadHistory(id);
      if (!current() || !history) return;
      await this.sends.get(id);
      if (current()) this.dispatch({ type: "mergeHistory", mainThreadId: id, ...history });
    } catch (error) {
      // Activation recovery is best effort; explicit actions retain visible errors.
      if (explicit && current()) this.dispatch({ type: "setError", mainThreadId: id, error: errorMessage(error) });
    }
  }
  private async poll(): Promise<void> {
    const id = this.options.activeThreadId, key = this.key(), ipc = this.ipc(), lifetime = this.lifetime;
    const entry = id ? this.store.byThread[id] : undefined;
    if (!id || !key || !ipc || this.sendDisabledReason || !entry?.open || entry.summary?.status !== "running" || this.polling) return;
    const reset = this.resets.get(key); this.polling = true;
    try {
      const history = await ipc.getSideThreadHistory(id);
      await this.sends.get(id);
      if (history && this.lifetime === lifetime && this.key() === key && this.resets.get(key) === reset) this.dispatch({ type: "mergeHistory", mainThreadId: id, ...history });
    } catch {
      // Events are authoritative; polling only repairs missed terminal delivery.
    } finally { this.polling = false; }
  }
  async open(): Promise<void> {
    const id = this.options.activeThreadId;
    if (!id || this.options.disabled) return;
    this.announced.delete(id); this.dispatch({ type: "open", mainThreadId: id });
    this.dispatch({ type: "setError", mainThreadId: id, error: undefined });
    await this.refresh(true);
  }
  close(): void { if (this.options.activeThreadId) this.dispatch({ type: "close", mainThreadId: this.options.activeThreadId }); }
  toggle(): void { if (this.options.activeThreadId && this.store.byThread[this.options.activeThreadId]?.open) this.close(); else void this.open(); }
  setDraft(draft: string): void { if (this.options.activeThreadId) this.dispatch({ type: "setDraft", mainThreadId: this.options.activeThreadId, draft }); }
  setDraftSelection(selection: SideThreadSelection | undefined): void { if (this.options.activeThreadId) this.dispatch({ type: "setDraftSelection", mainThreadId: this.options.activeThreadId, selection: selection && structuredClone(selection) }); }
  sendMessage(prompt: string, commandID = this.options.commandID): Promise<boolean> {
    const id = this.options.activeThreadId, key = this.key(), ipc = this.ipc(), lifetime = this.lifetime;
    const trimmed = prompt.trim(), entry = id ? this.store.byThread[id] : undefined;
    if (!id || !key || !ipc || this.sendDisabledReason || !trimmed || entry?.streaming || this.sends.has(id)) return Promise.resolve(false);
    const selection = entry?.draftSelection && structuredClone(entry.draftSelection), reset = this.resets.get(key);
    const optimisticID = `local-${crypto.randomUUID()}`;
    this.announced.delete(id);
    this.dispatch({ type: "appendMessage", mainThreadId: id, message: { id: optimisticID, side_thread_id: entry?.summary?.side_thread_id ?? "", role: "user", text: trimmed, selection, created_at: new Date().toISOString() } });
    this.dispatch({ type: "setDraft", mainThreadId: id, draft: "" }); this.dispatch({ type: "setDraftSelection", mainThreadId: id, selection: undefined });
    this.dispatch({ type: "setStreaming", mainThreadId: id, streaming: true }); this.dispatch({ type: "setError", mainThreadId: id, error: undefined });
    const current = () => this.lifetime === lifetime && this.resets.get(key) === reset;
    const task = (async () => {
      try {
        const result = await ipc.sendSideThreadMessage({ main_thread_id: id, prompt: trimmed, ...(commandID ? { command_id: commandID } : {}), ...(selection ? { selection } : {}) });
        if (!current()) return true;
        this.dispatch({ type: "mergeSummary", mainThreadId: id, summary: result.summary });
        this.dispatch({ type: "removeMessage", mainThreadId: id, messageId: result.user_message_id });
        this.dispatch({ type: "updateMessage", mainThreadId: id, messageId: optimisticID, patch: { id: result.user_message_id, side_thread_id: result.summary.side_thread_id } });
        return true;
      } catch (error) {
        if (!current()) return false;
        this.dispatch({ type: "removeMessage", mainThreadId: id, messageId: optimisticID }); this.dispatch({ type: "setStreaming", mainThreadId: id, streaming: false });
        this.dispatch({ type: "setError", mainThreadId: id, error: errorMessage(error) });
        const latest = this.store.byThread[id];
        if (!latest?.draft && !latest?.draftSelection) {
          this.dispatch({ type: "setDraft", mainThreadId: id, draft: trimmed }); this.dispatch({ type: "setDraftSelection", mainThreadId: id, selection });
        }
        return false;
      }
    })();
    this.sends.set(id, task);
    void task.finally(() => { if (this.sends.get(id) === task) this.sends.delete(id); });
    return task;
  }
  async interrupt(): Promise<void> {
    const id = this.options.activeThreadId, ipc = this.ipc(), lifetime = this.lifetime;
    if (!id || !ipc) return;
    this.announced.delete(id); this.dispatch({ type: "setError", mainThreadId: id, error: undefined });
    try { await ipc.interruptSideThread(id); }
    catch (error) { if (lifetime === this.lifetime) this.dispatch({ type: "setError", mainThreadId: id, error: errorMessage(error) }); }
  }
  async reset(): Promise<void> {
    const id = this.options.activeThreadId, cwd = this.options.activeContext?.cwd, ipc = this.ipc(), lifetime = this.lifetime;
    if (!id || !cwd || !ipc || this.sendDisabledReason) return;
    this.announced.delete(id); this.dispatch({ type: "setError", mainThreadId: id, error: undefined });
    try { await ipc.resetSideThread(id); if (lifetime === this.lifetime) { this.invalidateReset(id, cwd); this.dispatch({ type: "reset", mainThreadId: id }); } }
    catch (error) { if (lifetime === this.lifetime) this.dispatch({ type: "setError", mainThreadId: id, error: errorMessage(error) }); }
  }
}

/** Compatibility adapter while default UI moves to the application service. */
export function useSideThreadController(options: SideThreadControllerOptions): SideThreadController {
  useI18n();
  const owner = useRef<SideThreadOwner | null>(null);
  if (!owner.current) owner.current = new SideThreadOwner(options);
  const value = owner.current;
  useEffect(() => value.configure(options), [value, options.activeThreadId, options.activeContext, options.ipc, options.commandID, options.disabled, options.disabledReason]);
  useEffect(() => value.start(), [value]);
  const store = useSyncExternalStore(value.subscribe, value.getSnapshot);
  return { entry: options.activeThreadId ? store.byThread[options.activeThreadId] : undefined,
    open: () => { void value.open(); }, close: () => value.close(), toggle: () => value.toggle(),
    setDraft: draft => value.setDraft(draft), setDraftSelection: selection => value.setDraftSelection(selection),
    sendMessage: (prompt, commandID) => { void value.sendMessage(prompt, commandID); },
    interrupt: () => { void value.interrupt(); }, reset: () => { void value.reset(); }, sendDisabledReason: value.sendDisabledReason };
}
