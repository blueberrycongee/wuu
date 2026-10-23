import { randomUUID } from "node:crypto";
import type { ExtensionInventoryRecord, InitializeResult, PluginDesktopActivationParams } from "../shared/protocol";

type ProcessRequest = <T>(method: string, params?: unknown) => Promise<T>;

/** One renderer document. Graph routes retain the process that published them. */
export class PluginDesktopActivations {
  private readonly rendererID: string;
  private readonly workdirs = new Map<string, ProcessRequest>();
  private readonly graphs = new Map<string, ProcessRequest>();
  private readonly processes = new Set<ProcessRequest>();
  private pending: Promise<void> = Promise.resolve();
  private disconnected = false;
  private disconnecting?: Promise<void>;

  constructor(owner: number | string) { this.rendererID = `${owner}:${randomUUID()}`; }

  async initialize(workdir: string, request: ProcessRequest, params: unknown): Promise<InitializeResult> {
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    this.workdirs.set(workdir, request);
    const result = await request<InitializeResult>("initialize", params);
    if (this.disconnected) throw new Error("Renderer document is disconnected");
    this.bindInventory(request, result.extension_inventory ?? []);
    return result;
  }

  observeInventory(workdir: string, inventory: readonly ExtensionInventoryRecord[]): void {
    const request = this.workdirs.get(workdir);
    if (this.disconnected || !request) return;
    this.bindInventory(request, inventory);
  }

  private bindInventory(request: ProcessRequest, inventory: readonly ExtensionInventoryRecord[]): void {
    for (const plugin of inventory) {
      if (plugin.desktop && plugin.graph_generation && !this.graphs.has(plugin.graph_generation)) {
        this.graphs.set(plugin.graph_generation, request);
      }
    }
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
    if (!request) throw new Error("Desktop activation graph has no owning process");
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
      this.workdirs.clear();
      this.processes.clear();
      const failure = results.find(result => result.status === "rejected");
      if (failure?.status === "rejected") throw failure.reason;
    });
    return this.disconnecting;
  }
}
