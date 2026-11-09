import { describe, expect, it, vi } from "vitest";
import type { AppServerClientPool, AppServerClient } from "./appServerClients";
import type { PluginClientCancelParams, PluginClientRequestParams, RuntimeContext } from "../shared/protocol";
import { PluginRuntimeRequests } from "./pluginRuntimeRequests";

const context: RuntimeContext = { kind: "project", project_id: "workspace", cwd: "/workspace" };
const identity: PluginClientCancelParams = { id: "consumer", fingerprint: "source", graph_generation: "first", request_id: "request" };
function process() {
  let finish!: (result: unknown) => void;
  const calls = vi.fn((method: string, _params?: unknown): Promise<unknown> => method === "plugin/client/cancel"
    ? Promise.resolve({ cancelled: true }) : new Promise(resolve => { finish = resolve; }));
  return { calls, finish: (result: unknown) => finish(result), request: calls as AppServerClient["request"] };
}

describe("PluginRuntimeRequests", () => {
  it("routes a scoped request without sending the desktop-only composition profile to the server", async () => {
    const target = process();
    const requests = new PluginRuntimeRequests({} as AppServerClientPool);
    const params: PluginClientRequestParams = {
      ...identity, method: "read", user_space_id: "local", product_domain: "personal", composition_profile: "personal",
    };
    const pending = requests.request("window", target.request, params);
    expect(target.calls.mock.calls[0][1]).toEqual({
      ...identity, request_id: expect.any(String), method: "read", user_space_id: "local", product_domain: "personal",
    });
    target.finish({ result: "ok" });
    await pending;
  });

  it("keeps service calls and cancellation on their captured process and graph", async () => {
    const first = process(), second = process();
    let current = first;
    const bindContextRequest = vi.fn(() => current.request);
    const requests = new PluginRuntimeRequests({ bindContextRequest } as unknown as AppServerClientPool);
    const params: PluginClientRequestParams = {
      ...identity, service: "catalog", method: "read", input: { item: "one" }, workspace_id: "workspace", workspace_root: "/workspace",
    };
    const pending = requests.request("window", context, params);
    const wire = first.calls.mock.calls[0][1] as PluginClientRequestParams;
    expect(wire).toEqual({ ...identity, request_id: expect.any(String), service: "catalog", method: "read", input: params.input });
    expect(wire.request_id).not.toBe(identity.request_id);
    current = second;
    await expect(requests.cancel("window", { ...identity, graph_generation: "second" })).resolves.toEqual({ cancelled: false });
    await expect(requests.cancel("another-window", identity)).resolves.toEqual({ cancelled: false });
    expect(first.calls).toHaveBeenCalledTimes(1);
    await expect(requests.cancel("window", identity)).resolves.toEqual({ cancelled: true });
    await requests.cancel("window", identity);
    expect(first.calls.mock.calls[1]).toEqual(["plugin/client/cancel", { ...identity, request_id: wire.request_id }]);
    expect(first.calls).toHaveBeenCalledTimes(2);
    expect(second.calls).not.toHaveBeenCalled();
    first.finish({ result: "first" });
    await expect(pending).resolves.toEqual({ result: "first" });
    await expect(requests.cancel("window", identity)).resolves.toEqual({ cancelled: false });
    const nextIdentity = { ...identity, graph_generation: "second" };
    const next = requests.request("window", context, { ...params, ...nextIdentity });
    await expect(requests.cancel("window", identity)).resolves.toEqual({ cancelled: false });
    expect(second.calls).toHaveBeenCalledTimes(1);
    const nextWire = second.calls.mock.calls[0][1] as PluginClientRequestParams;
    requests.closeOwner("window");
    requests.closeOwner("window");
    expect(second.calls.mock.calls[1]).toEqual(["plugin/client/cancel", { ...nextIdentity, request_id: nextWire.request_id }]);
    expect(second.calls).toHaveBeenCalledTimes(2);
    second.finish({ result: "second" });
    await next;
    expect(bindContextRequest).toHaveBeenCalledTimes(2);
  });
});
