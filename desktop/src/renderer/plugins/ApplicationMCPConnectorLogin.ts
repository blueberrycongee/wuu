import type { MCPServerStatus, ExtensionInventoryRecord } from "../../shared/protocol";
import type { ApplicationWorkbenchV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";

/** Process-local observation of one dispatched preference write, never persisted to storage. */
export type MCPConnectorLoginIntent = {
  readonly name: string; readonly context: string; readonly view: string;
  readonly source: string; readonly resource: string; readonly graph: string; readonly expires: number;
  readonly requiresPublication: boolean;
  readonly committed: Promise<boolean>;
  readonly disposition: { value: "pending" | "cancelled" | "consumed" };
};

/** Only presentation intent crosses a stopped writer; OAuth starts on fresh controls. */
export class ApplicationMCPConnectorLogin {
  private pending?: MCPConnectorLoginIntent;
  private revision = 0;
  private requesting = false;
  private requestOwner?: { context: string; view: string };
  private readonly stopped = new AbortController();
  constructor(private readonly host: {
    context(): string;
    presentation(): string;
    inventory(): readonly ExtensionInventoryRecord[];
    workbench(): ApplicationWorkbenchV1;
  }) {}

  private view(): string {
    const snapshot = this.host.workbench().getSnapshot();
    const active = snapshot.views.find(view => view.id === snapshot.activeViewByRegion.primary);
    return JSON.stringify([this.host.presentation(), active ? [active.id, active.pluginId, active.viewTypeId, active.context] : null]);
  }
  private owner(server: MCPServerStatus): { source: string; graph: string } | undefined {
    if (!server.connector || !server.configurable || server.plugin_active !== true || server.connector.setup_required?.trim()) return;
    const plugin = this.host.inventory().find(item => item.kind === "plugin" && item.provenance.plugin_id === server.plugin_id);
    if (!plugin?.fingerprint || !plugin.graph_generation || plugin.enabled === false) return;
    return { source: JSON.stringify([plugin.id, plugin.fingerprint, plugin.provenance, plugin.package_source]), graph: plugin.graph_generation };
  }
  reconcile(): void {
    if (this.requestOwner && (this.requestOwner.context !== this.host.context() || this.requestOwner.view !== this.view())) this.cancel();
    if (this.pending && (this.pending.expires <= Date.now() || this.pending.context !== this.host.context() || this.pending.view !== this.view())) this.cancel();
  }
  cancel(): void {
    this.revision++;
    this.requestOwner = undefined;
    if (this.pending) this.pending.disposition.value = "cancelled";
    this.pending = undefined;
  }
  getPending(): string | undefined {
    this.reconcile();
    return this.pending?.disposition.value === "pending" ? this.pending.name : undefined;
  }
  suspend(): void { this.stopped.abort(); }
  checkpoint(): MCPConnectorLoginIntent | undefined {
    this.reconcile();
    return this.pending?.disposition.value === "pending" ? this.pending : undefined;
  }
  restore(intent?: MCPConnectorLoginIntent): void {
    this.pending = intent;
    this.reconcile();
  }

  async request(name: string): Promise<void> {
    this.stopped.signal.throwIfAborted();
    this.reconcile();
    if (this.requesting || this.pending?.disposition.value === "pending") throw new Error("Finish or cancel the current connector sign-in first.");
    this.requesting = true;
    try {
      const revision = this.revision;
      const context = this.host.context(), view = this.view();
      this.requestOwner = { context, view };
      const current = () => {
        this.stopped.signal.throwIfAborted();
        if (revision !== this.revision || context !== this.host.context() || view !== this.view()) throw new Error("Connector sign-in owner changed.");
      };
      const list = await window.wuu.listMCPServers(); current();
      const server = list.servers.find(server => server.name === name);
      const owner = server && this.owner(server);
      if (!server?.resource_url || !owner) throw new Error("Connector is no longer available.");
      const definition = await window.wuu.readMCPDefinition(name); current();
      if (this.owner(server)?.source !== owner.source) throw new Error("Connector is no longer available.");
      let settle!: (committed: boolean) => void;
      const committed = new Promise<boolean>(resolve => { settle = resolve; });
      const requiresPublication = server.configuration_enabled !== true || definition.config.enabled !== true;
      this.pending = { name, context, view, ...owner, resource: server.resource_url, expires: Date.now() + 60_000,
        requiresPublication, committed, disposition: { value: "pending" } };
      try {
        if (requiresPublication) await window.wuu.configureMCPServers({ servers: { [name]: { enabled: true } }, revision: definition.revision });
        settle(true);
      } catch (error) {
        this.cancel();
        settle(false);
        throw error;
      }
      // No old-generation callback opens a browser or writes successor state.
    } finally { this.requesting = false; this.requestOwner = undefined; }
  }

  async take(): Promise<MCPServerStatus | undefined> {
    this.reconcile();
    const intent = this.pending;
    if (!intent || intent.disposition.value !== "pending" || this.stopped.signal.aborted) return;
    if (Date.now() >= intent.expires) { this.cancel(); return; }
    const committed = await new Promise<boolean>(resolve => {
      const signal = this.stopped.signal;
      const finish = (value: boolean) => { clearTimeout(timer); signal.removeEventListener("abort", abort); resolve(value); };
      const abort = () => finish(false);
      const timer = setTimeout(() => finish(false), Math.max(0, intent.expires - Date.now()));
      this.stopped.signal.addEventListener("abort", abort, { once: true });
      void intent.committed.then(finish);
    });
    if (this.stopped.signal.aborted) return;
    this.reconcile();
    if (this.pending !== intent || intent.disposition.value !== "pending") return;
    if (!committed) { this.cancel(); return; }
    const list = await window.wuu.listMCPServers();
    if (this.stopped.signal.aborted) return;
    this.reconcile();
    if (this.pending !== intent || intent.disposition.value !== "pending") return;
    const server = list.servers.find(server => server.name === intent.name);
    const owner = server && this.owner(server);
    if (!server?.configuration_enabled || !owner || owner.source !== intent.source || server.resource_url !== intent.resource) { this.cancel(); return; }
    if (intent.requiresPublication && owner.graph === intent.graph) return; // A changed preference requires a published successor.
    intent.disposition.value = "consumed";
    this.pending = undefined;
    return server;
  }
}
