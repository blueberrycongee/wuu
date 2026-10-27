import { randomUUID } from "node:crypto";
import type { AppServerClientPool, AppServerClient } from "./appServerClients";
import type { AppServerResponse, PluginClientCancelParams, PluginClientCancelResult, PluginClientRequestParams, PluginClientRequestResult, RuntimeContext } from "../shared/protocol";

/** Bind cancellation to the process that admitted a request, across relocation
 * and re-registration. Window and remote-peer owners follow the same rule. */
export class PluginRuntimeRequests {
  private readonly owners = new Map<string, Map<string, {
    id: string;
    fingerprint: string;
    graph_generation: string;
    cancel: () => Promise<PluginClientCancelResult>;
  }>>();

  constructor(private readonly pool: AppServerClientPool) {}

  async request(
    owner: string,
    context: RuntimeContext | AppServerClient["request"],
    params: PluginClientRequestParams,
    onResponse?: (response: AppServerResponse) => void,
    onOwnerCreated?: () => void,
  ): Promise<PluginClientRequestResult> {
    const { workspace_id: _workspaceID, workspace_root: _workspaceRoot, ...request } = params;
    const requestInOwner = typeof context === "function" ? context : this.pool.bindContextRequest(context);
    if (params.request_id === undefined) {
      return requestInOwner<PluginClientRequestResult>("plugin/client/request", request, onResponse);
    }
    if (typeof params.request_id !== "string" || params.request_id.length > 128 || !params.request_id.trim()) {
      throw new Error("Plugin request ID must contain 1 to 128 characters");
    }
    let requests = this.owners.get(owner);
    if (!requests) {
      requests = new Map();
      this.owners.set(owner, requests);
      onOwnerCreated?.();
    }
    if (requests.has(params.request_id)) throw new Error("Plugin request ID is already active");
    const identity = { id: params.id, fingerprint: params.fingerprint, graph_generation: params.graph_generation, request_id: randomUUID() };
    let cancellation: Promise<PluginClientCancelResult> | undefined;
    requests.set(params.request_id, {
      id: params.id,
      fingerprint: params.fingerprint,
      graph_generation: params.graph_generation,
      cancel: () => cancellation ??= requestInOwner<PluginClientCancelResult>("plugin/client/cancel", identity),
    });
    try {
      return await requestInOwner<PluginClientRequestResult>(
        "plugin/client/request", { ...request, request_id: identity.request_id }, onResponse,
      );
    } finally {
      requests.delete(params.request_id);
    }
  }

  cancel(owner: string, params: PluginClientCancelParams): Promise<PluginClientCancelResult> {
    const request = params && this.owners.get(owner)?.get(params.request_id);
    if (!request || request.id !== params.id || request.fingerprint !== params.fingerprint || request.graph_generation !== params.graph_generation) {
      return Promise.resolve({ cancelled: false });
    }
    return request.cancel();
  }

  closeOwner(owner: string): void {
    const requests = this.owners.get(owner);
    this.owners.delete(owner);
    for (const request of requests?.values() ?? []) {
      void request.cancel().catch((error: unknown) => console.error("Plugin runtime cancellation failed", error));
    }
  }
}
