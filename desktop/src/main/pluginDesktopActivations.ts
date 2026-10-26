import type { WebContents } from "electron";
import { randomUUID } from "node:crypto";
import { resolve } from "node:path";
import type { AppServerResponse, ExtensionInventoryRecord, InitializeResult, PluginDesktopActivationParams } from "../shared/protocol";

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
