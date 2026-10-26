import type { InitializeResult, ServerEvent } from "../../shared/protocol";
import { subscribeServerEvents } from "../ServerEvents";

export interface DesktopBootstrapSnapshot {
  readonly initialized?: InitializeResult;
  readonly connectionGeneration: number;
  readonly error?: string;
}
/** Transport bootstrap only. Business state, policy, subscriptions and writers belong to providers. */
export class DesktopBootstrap {
  private state: DesktopBootstrapSnapshot = { connectionGeneration: 0 };
  private listeners = new Set<() => void>();
  private epoch = 0;
  getSnapshot = (): DesktopBootstrapSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  private publish(state: DesktopBootstrapSnapshot): void { this.state = state; for (const listener of this.listeners) listener(); }
  start(): () => void {
    let active = true, publicationSequence = 0, observedInitialization = 0, latestRequest = 0, requestStart = 0;
    const publications = new Map<string, { sequence: number; value: Partial<InitializeResult> }>();
    const accept = (initialized: InitializeResult, since: number) => {
      const publication = publications.get(initialized.workspace_root);
      const next = publication && publication.sequence > since ? { ...initialized, ...publication.value } : { ...initialized };
      // Business initialization reads the same transport too. Preserve inventory identity
      // for an unchanged graph so it cannot cancel its own in-progress activation.
      const previous = this.state.initialized;
      if (previous?.workspace_root === next.workspace_root
        && JSON.stringify(previous.extension_inventory) === JSON.stringify(next.extension_inventory)) {
        next.extension_inventory = previous.extension_inventory;
      }
      this.publish({ ...this.state, initialized: next, error: undefined });
    };
    const initializationOff = window.wuu.onRuntimeInitialization?.(event => {
      if (!active) return;
      observedInitialization++;
      if (event.phase === "started") { latestRequest = event.requestId; requestStart = publicationSequence; return; }
      if (event.requestId === latestRequest) accept(event.initialized, requestStart);
    });
    const initialize = async (restore = false) => {
      const epoch = ++this.epoch, observed = observedInitialization, since = publicationSequence;
      try {
        const initialized = await window.wuu.initialize();
        if (!active || epoch !== this.epoch) return;
        // Older adapters do not expose transport observations; their own bootstrap
        // call still gets the same race-safe inventory publication merge.
        if (observedInitialization === observed) accept(initialized, since);
        if (restore) this.publish({ ...this.state, connectionGeneration: this.state.connectionGeneration + 1 });
      } catch (error) { if (active && epoch === this.epoch) this.publish({ ...this.state, error: String(error) }); }
    };
    const receive = (event: ServerEvent) => {
      if (event.kind !== "notification" || event.message.method !== "plugin/inventory/changed") return;
      const params = event.message.params as Partial<InitializeResult>;
      const value = {
        ...("extension_inventory" in params ? { extension_inventory: params.extension_inventory } : {}),
        ...("application_shell" in params ? { application_shell: params.application_shell } : {}),
        ...("application_surfaces" in params ? { application_surfaces: params.application_surfaces } : {}),
      };
      publications.set(event.workdir, { sequence: ++publicationSequence, value });
      if (this.state.initialized?.workspace_root === event.workdir) accept(this.state.initialized, publicationSequence - 1);
    };
    const off = subscribeServerEvents(receive);
    const restoreOff = window.wuu.onRuntimeRestore?.(() => initialize(true));
    void initialize();
    return () => { active = false; this.epoch++; off(); restoreOff?.(); initializationOff?.(); publications.clear(); };
  }
}
export const desktopBootstrap = new DesktopBootstrap();
