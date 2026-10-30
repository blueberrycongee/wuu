import type { MCPServerStatus, ExtensionInventoryRecord, MCPLoginStartResult, MCPLoginPollResult } from "../../shared/protocol";
import { hostSupports } from "../HostCapabilities";
import { usesAutomaticMCPLogin } from "../MCPLoginTransport";
import type { ApplicationWorkbenchV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";

type NativeLoginContinuation = {
  start: Promise<MCPLoginStartResult>;
  poll?: Promise<MCPLoginPollResult>;
  terminal?: MCPLoginPollResult;
  failed: boolean;
  cancelled: boolean;
  cancel(): void;
};

/** Process-local continuation only; never serialized. OAuth state, codes and credentials remain native. */
export type MCPConnectorLoginIntent = {
  readonly name: string; readonly context: string; readonly view: string;
  readonly source: string; readonly resource: string; readonly graph: string; expires: number;
  readonly requiresPublication: boolean;
  readonly committed: Promise<boolean>;
  readonly disposition: { value: "pending" | "cancelled" | "consumed" };
  native?: NativeLoginContinuation;
};

/** Presentation replacement reattaches to one native operation, never replays it. */
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

  private trace(phase: string, flags: Record<string, boolean> = {}): void {
    // Observational lifecycle facts only; never paths, names, provider URLs,
    // credentials or callback material. Receivers cannot alter the intent.
    window.dispatchEvent(new CustomEvent("wuu:mcp-connector-handoff", { detail: { phase, ...flags } }));
  }

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
  private sameSource(intent: MCPConnectorLoginIntent): boolean {
    return this.host.inventory().some(plugin => plugin.kind === "plugin" && plugin.enabled !== false
      && JSON.stringify([plugin.id, plugin.fingerprint, plugin.provenance, plugin.package_source]) === intent.source);
  }
  reconcile(): void {
    const owner = this.pending ?? this.requestOwner;
    if (owner && (owner.context !== this.host.context() || owner.view !== this.view() || !!this.pending && this.pending.expires <= Date.now())) {
      this.trace("invalidated", { contextChanged: owner.context !== this.host.context(), viewChanged: owner.view !== this.view(), expired: !!this.pending && this.pending.expires <= Date.now() });
    }
    if (this.requestOwner && (this.requestOwner.context !== this.host.context() || this.requestOwner.view !== this.view())) this.cancel();
    if (this.pending && (this.pending.expires <= Date.now() || this.pending.context !== this.host.context() || this.pending.view !== this.view() || !this.sameSource(this.pending))) this.cancel();
  }
  cancel(): void {
    this.revision++;
    this.requestOwner = undefined;
    if (this.pending) {
      this.pending.disposition.value = "cancelled";
      this.pending.native?.cancel();
    }
    this.pending = undefined;
  }
  private active(intent = this.pending): intent is MCPConnectorLoginIntent {
    return !!intent && (intent.disposition.value === "pending" || intent.disposition.value === "consumed" && !!intent.native);
  }
  getPending(): string | undefined {
    this.reconcile();
    return this.active() ? this.pending!.name : undefined;
  }
  suspend(): void { this.trace("suspend", { pending: !!this.pending }); this.stopped.abort(); }
  checkpoint(): MCPConnectorLoginIntent | undefined {
    this.reconcile();
    this.trace("checkpoint", { pending: this.active() });
    return this.active() ? this.pending : undefined;
  }
  restore(intent?: MCPConnectorLoginIntent): void {
    this.pending = intent;
    this.trace("restore", { pending: this.active(intent) });
    this.reconcile();
  }

  async request(name: string): Promise<void> {
    this.trace("request");
    this.stopped.signal.throwIfAborted();
    this.reconcile();
    if (this.requesting || this.active()) throw new Error("Finish or cancel the current connector sign-in first.");
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
      this.trace("admitted", { requiresPublication });
      try {
        if (requiresPublication) await window.wuu.configureMCPServers({ servers: { [name]: { enabled: true } }, revision: definition.revision });
        settle(true);
        this.trace("receipt", { committed: true });
      } catch (error) {
        this.trace("receipt", { committed: false });
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
    this.trace("take", { pending: intent?.disposition.value === "pending", stopped: this.stopped.signal.aborted });
    if (!this.active(intent) || this.stopped.signal.aborted) return;
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
    if (this.pending !== intent || !this.active(intent)) return;
    if (!committed) { this.cancel(); return; }
    const list = await window.wuu.listMCPServers();
    if (this.stopped.signal.aborted) return;
    this.reconcile();
    if (this.pending !== intent || !this.active(intent)) return;
    const server = list.servers.find(server => server.name === intent.name);
    const owner = server && this.owner(server);
    this.trace("validated", { server: !!server, enabled: server?.configuration_enabled === true, owner: !!owner,
      sameSource: owner?.source === intent.source, sameResource: server?.resource_url === intent.resource,
      sameGraph: owner?.graph === intent.graph, requiresPublication: intent.requiresPublication });
    if (!server?.configuration_enabled || !owner || owner.source !== intent.source || server.resource_url !== intent.resource) { this.cancel(); return; }
    if (intent.requiresPublication && owner.graph === intent.graph) return; // A changed preference requires a published successor.
    if (intent.disposition.value === "pending") {
      intent.disposition.value = "consumed";
      if (usesAutomaticMCPLogin(server, hostSupports("startMCPLogin") && typeof window.wuu.startMCPLogin === "function")) {
        // Capture the raw native receipt before any scoped facade can retire.
        // The successor can attach even if this take() result is discarded.
        intent.native = this.beginNative(intent);
      } else {
        // Manual OAuth still belongs to the existing dialog. Never checkpoint
        // its state/code/authorization URL; interruption requires explicit retry.
        this.pending = undefined;
      }
      this.trace("consumed");
    }
    return server;
  }

  private beginNative(intent: MCPConnectorLoginIntent): NativeLoginContinuation {
    const api = window.wuu;
    let loginID: string | undefined, cancellation: Promise<unknown> | undefined;
    let expiry: ReturnType<typeof setTimeout>;
    const cancelNative = () => {
      if (loginID && !cancellation && native.terminal?.status !== "success") {
        cancellation = api.cancelMCPLogin(loginID).catch(() => undefined);
      }
    };
    const native: NativeLoginContinuation = {
      start: undefined!, failed: false, cancelled: false,
      cancel: () => { native.cancelled = true; clearTimeout(expiry); cancelNative(); },
    };
    intent.expires = Date.now() + 10 * 60_000;
    expiry = setTimeout(() => { intent.disposition.value = "cancelled"; native.cancel(); }, 10 * 60_000);
    native.start = Promise.resolve().then(() => {
      if (native.cancelled) throw new Error("Connector sign-in cancelled.");
      return api.startMCPLogin(intent.name);
    }).then(result => {
      loginID = result.login_id;
      if (native.cancelled) { cancelNative(); throw new Error("Connector sign-in cancelled."); }
      return result;
    }, error => { native.failed = true; clearTimeout(expiry); throw error; });
    void native.start.catch(() => undefined);
    return native;
  }

  private continuation(): NativeLoginContinuation {
    this.stopped.signal.throwIfAborted();
    this.reconcile();
    const native = this.pending?.native;
    if (!this.active() || !native || native.cancelled) throw new Error("Connector sign-in is no longer available.");
    return native;
  }
  async start(): Promise<MCPLoginStartResult> { return this.continuation().start; }
  async poll(): Promise<MCPLoginPollResult> {
    const native = this.continuation();
    if (native.terminal) return native.terminal;
    if (!native.poll) {
      const api = window.wuu;
      native.poll = native.start.then(result => {
        if (native.cancelled) throw new Error("Connector sign-in cancelled.");
        return api.pollMCPLogin(result.login_id);
      }).then(result => {
        if (result.status !== "pending") native.terminal = result;
        return result;
      }, error => {
        native.terminal = { status: "error", error: error instanceof Error ? error.message : String(error) };
        return native.terminal;
      }).finally(() => { native.poll = undefined; });
    }
    return native.poll;
  }
  async retry(): Promise<void> {
    const native = this.continuation();
    if (!native.failed && native.terminal?.status !== "error") throw new Error("Finish or cancel the current connector sign-in first.");
    const intent = this.pending!;
    const server = await this.take();
    this.stopped.signal.throwIfAborted();
    if (!server || this.pending !== intent || intent.native !== native) return;
    native.cancel();
    intent.native = this.beginNative(intent);
  }

}
