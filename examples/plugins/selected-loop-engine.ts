// Run: wuu plugin dev ./selected-loop-engine.ts
// Select this plugin for engine.wuu@1. runLoop delegates to the selected
// driver service, so replacing the engine never reintroduces a built-in loop.
import {
  engineGateway, engineServiceDescriptor, requireEngineGateway,
  type EngineBinding, type EngineRunParams, type EngineSessionParams,
  type RuntimePlugin,
} from "@wuu/plugin-sdk";

interface BoundSession {
  readonly binding: EngineBinding;
  controller?: AbortController;
  done?: Promise<void>;
  closed: boolean;
}
const sessions = new Map<string, BoundSession>();
let nextID = 0;
const plugin: RuntimePlugin = {
  initialize: () => ({
    protocol_version: 3, lifecycle_version: 2,
    provided_services: [engineServiceDescriptor("wuu")],
    required_services: [requireEngineGateway()],
  }),
  async invokeService({ method, params }, host, execution) {
    if (method === "descriptor") return {
      id: "wuu", version: "1.0.0", enabled: true,
      capabilities: ["host-managed-continuation", "canonical-history", "host-managed-steering"],
    };
    if (method === "describe") return { binary_ok: true, display_name: "Selected loop engine", protocol: "plugin" };
    if (method === "bind") {
      const id = `session-${++nextID}`;
      sessions.set(id, { binding: params as EngineBinding, closed: false });
      return { session_id: id };
    }
    const id = (params as EngineSessionParams).session_id;
    const session = sessions.get(id);
    if (!session) throw new Error("engine session unavailable");
    if (method === "close") {
      session.closed = true;
      session.controller?.abort();
      await session.done;
      sessions.delete(id);
      return {};
    }
    if (method === "interrupt") { session.controller?.abort(); return {}; }
    if (method !== "run") throw new Error(`unsupported engine operation ${method}`);
    if (session.closed || session.done) throw new Error("engine session closed or already running");
    session.controller = new AbortController();
    let drained!: () => void;
    session.done = new Promise<void>(resolve => { drained = resolve; });
    try {
      const gateway = engineGateway(host, session.binding.gateway_id, {
        signal: AbortSignal.any([execution.signal, session.controller.signal]),
      });
      return await gateway.runLoop((params as EngineRunParams).messages);
    } finally {
      session.controller = undefined;
      session.done = undefined;
      drained();
    }
  },
};
export default plugin;
