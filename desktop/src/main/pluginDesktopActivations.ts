import type { WebContents } from "electron";
import { randomUUID } from "node:crypto";
import { resolve } from "node:path";
import type { AppServerResponse, ExtensionInventoryRecord, InitializeResult, PluginDesktopActivationParams } from "../shared/protocol";
import { AppServerResponseError } from "./appServerErrors";

export class DesktopGraphOwnershipError extends Error {
  readonly code = "WUU_DESKTOP_GRAPH_OWNER_MISSING";
  readonly recovery = "reinitialize-required";
  constructor(readonly graphGeneration: string) {
    super("Desktop activation graph has no owning process; fresh authoritative initialization is required");
  }
}

type ProcessRequest = <T>(method: string, params?: unknown, onResponse?: (response: AppServerResponse) => void) => Promise<T>;

/** One renderer document. Graph routes retain the process that published them. */
export class PluginDesktopActivations {
  private readonly rendererID: string;
  private readonly workdirs = new Map<string, ProcessRequest>();
  private readonly graphPlugins = new Map<string, Map<string, string>>();
  private readonly scopedBindings = new Map<string, Promise<ProcessRequest>>();
  private readonly graphs = new Map<string, ProcessRequest>();
  private readonly graphWorkdirs = new Map<string, string>();
  private readonly processes = new Set<ProcessRequest>();
  private pending: Promise<void> = Promise.resolve();
  private disconnected = false;
  private recoveryAttempted = false;
  initializationRevision = 0;
  rendererRequestId = 0;
  private recovery?: Promise<void>;
  private disconnecting?: Promise<void>;

  constructor(owner: number | string) { this.rendererID = `${owner}:${randomUUID()}`; }

  async initialize(workdir: string, request: ProcessRequest, params: unknown): Promise<InitializeResult> {
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    this.initializationRevision++;
    this.workdirs.set(resolve(workdir), request);
    const result = await request<InitializeResult>("initialize", params);
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    this.bindInventory(workdir, request, result.extension_inventory ?? []);
    return result;
  }

  /** An exited process cannot authenticate later inventory solely by workspace path. */
  invalidateWorkspace(workdir: string): void { this.workdirs.delete(resolve(workdir)); }

  observeInventory(workdir: string, inventory: readonly ExtensionInventoryRecord[]): void {
    const request = this.workdirs.get(resolve(workdir));
    if (this.disconnected || !request) return;
    this.bindInventory(workdir, request, inventory);
  }

  private bindInventory(workdir: string, request: ProcessRequest, inventory: readonly ExtensionInventoryRecord[]): void {
    for (const plugin of inventory) {
      if (plugin.graph_generation && plugin.fingerprint) {
        let identities = this.graphPlugins.get(plugin.graph_generation);
        if (!identities) this.graphPlugins.set(plugin.graph_generation, identities = new Map());
        if (!identities.has(plugin.id)) identities.set(plugin.id, plugin.fingerprint);
      }
      if (plugin.desktop && plugin.graph_generation && !this.graphs.has(plugin.graph_generation)) {
        this.graphs.set(plugin.graph_generation, request);
        this.graphWorkdirs.set(plugin.graph_generation, resolve(workdir));
      }
    }
  }

  /** A graph belongs to its publishing process, never the window's later workspace. */
  runtimeRequest(graph: string, workspaceRoot?: string): ProcessRequest {
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    const request = this.graphs.get(graph);
    if (!request) throw new DesktopGraphOwnershipError(graph);
    if (workspaceRoot !== undefined && resolve(workspaceRoot) !== this.graphWorkdirs.get(graph)) {
      throw new Error("Plugin runtime graph belongs to a different workspace");
    }
    return request;
  }

  /** Scope changes require a fresh authoritative handshake, not a copied graph ID.
   * Keep failed bindings failed for this activation: an uncertain admission must
   * never become a replay into a replacement host. */
  async scopedRuntimeRequest(
    identity: { id: string; fingerprint: string; graph_generation: string },
    scope: string,
    acquire: () => Promise<{ workdir: string; request: ProcessRequest; params: unknown }>,
  ): Promise<ProcessRequest> {
    this.runtimeRequest(identity.graph_generation);
    if (this.graphPlugins.get(identity.graph_generation)?.get(identity.id) !== identity.fingerprint) {
      throw new Error("Plugin source identity does not match its activation graph");
    }
    const key = JSON.stringify([identity.graph_generation, identity.id, identity.fingerprint, scope]);
    let binding = this.scopedBindings.get(key);
    if (!binding) {
      binding = (async () => {
        const target = await acquire();
        this.runtimeRequest(identity.graph_generation);
        const result = await this.initialize(target.workdir, target.request, target.params);
        const plugin = result.extension_inventory?.find(candidate => candidate.id === identity.id);
        if (!plugin?.graph_generation || plugin.fingerprint !== identity.fingerprint || plugin.enabled === false) {
          throw new Error("Plugin target identity does not match its activation graph");
        }
        const request = this.runtimeRequest(plugin.graph_generation, target.workdir);
        // The target supplies runtime services only. UI activation and its
        // cleanup remain with the source graph; request cancellation is bound
        // separately by PluginRuntimeRequests.
        return async <T>(method: string, params?: unknown, response?: (value: AppServerResponse) => void): Promise<T> => {
          if (this.disconnected && method !== "plugin/client/cancel") throw new Error("Renderer document is disconnected");
          const input = params as typeof identity;
          if (!input || input.id !== identity.id || input.fingerprint !== identity.fingerprint || input.graph_generation !== identity.graph_generation) {
            throw new Error("Scoped plugin request identity changed");
          }
          try {
            return await request<T>(method, { ...input, graph_generation: plugin.graph_generation }, response);
          } catch (error) {
            // This rejection occurs before the server admits the operation.
            // Retire only this binding; never replay an already dispatched call.
            if (error instanceof AppServerResponseError && error.message === "plugin graph generation is no longer active"
              && this.scopedBindings.get(key) === binding) this.scopedBindings.delete(key);
            throw error;
          }
        };
      })();
      this.scopedBindings.set(key, binding);
    }
    return binding;
  }

  /** Admission only: callers dispatch once after this returns; transport failures never enter recovery. */
  async recoverRuntimeRequest(graph: string, workspaceRoot: string | undefined, initialize: () => Promise<void>): Promise<ProcessRequest> {
    try { return this.runtimeRequest(graph, workspaceRoot); }
    catch (error) { if (!(error instanceof DesktopGraphOwnershipError)) throw error; }
    await this.recoverGraph(graph, initialize);
    return this.runtimeRequest(graph, workspaceRoot);
  }

  /** One recovery handshake per document; its inventory must prove this exact graph. */
  async recoverGraph(graph: string, initialize: () => Promise<void>): Promise<void> {
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    if (!graph || typeof graph !== "string") throw new Error("Invalid desktop graph identity");
    if (this.graphs.has(graph)) return;
    if (!this.recoveryAttempted) {
      this.recoveryAttempted = true;
      this.recovery = Promise.resolve().then(initialize);
    }
    await this.recovery;
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    if (!this.graphs.has(graph)) throw new DesktopGraphOwnershipError(graph);
  }

  async report(params: PluginDesktopActivationParams): Promise<void> {
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    const { id, fingerprint, graph_generation, activation, phase, status, message } = params;
    if (!id || !fingerprint || !graph_generation || !Number.isSafeInteger(activation) || activation < 1
      || !["registration", "render", "cleanup"].includes(phase) || !["active", "failed", "disposed"].includes(status)
      || (message !== undefined && (typeof message !== "string" || message.length > 1024))) {
      throw new Error("Invalid desktop activation report");
    }
    const request = this.graphs.get(graph_generation);
    if (!request) throw new DesktopGraphOwnershipError(graph_generation);
    this.processes.add(request);
    const pending = this.pending.then(async () => {
      await request("plugin/desktop-activation", {
        renderer_id: this.rendererID, id, fingerprint, graph_generation, activation, phase, status,
        ...(message === undefined ? {} : { message }),
      });
    });
    this.pending = pending.catch(() => {});
    await pending;
  }

  disconnect(): Promise<void> {
    if (this.disconnecting) return this.disconnecting;
    this.disconnected = true;
    this.disconnecting = this.pending.then(async () => {
      const results = await Promise.allSettled([...this.processes].map(request => request("plugin/desktop-activation", {
        renderer_id: this.rendererID, disconnected: true,
      })));
      this.graphs.clear();
      this.graphPlugins.clear();
      this.scopedBindings.clear();
      this.graphWorkdirs.clear();
      this.workdirs.clear();
      this.processes.clear();
      const failure = results.find(result => result.status === "rejected");
      if (failure?.status === "rejected") throw failure.reason;
    });
    return this.disconnecting;
  }
}

/** Revoke ownership only when the document is actually replaced or destroyed. */
export function observeDesktopActivationDocument(contents: Pick<WebContents, "on" | "once">, disconnect: () => void): void {
  contents.on("did-navigate", disconnect);
  contents.on("render-process-gone", disconnect);
  contents.once("destroyed", disconnect);
}
