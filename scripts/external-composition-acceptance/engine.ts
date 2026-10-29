import {
  engineGateway, engineServiceDescriptor, requireEngineGateway,
  type EngineBinding, type EngineRunParams, type EngineSessionParams, type RuntimePlugin,
} from "@wuu/plugin-sdk";

// These fixture values are replaced in the copied, independent consumer only.
const label = "ACCEPTANCE_ENGINE_LABEL";
const endpoint = "ACCEPTANCE_CONTROL_ENDPOINT";
const sessions = new Map<string, {
  binding: EngineBinding; controller?: AbortController; done?: Promise<void>; closed: boolean;
}>();
let sequence = 0;

async function observe(event: object, signal?: AbortSignal): Promise<void> {
  const response = await fetch(endpoint, {
    method: "POST", body: JSON.stringify({ label, ...event }), signal,
    headers: { "content-type": "application/json" },
  });
  if (!response.ok) throw new Error(`acceptance control failed: ${response.status}`);
}

export default {
  concurrentServices: ["engine.wuu"],
  initialize: () => ({
    protocol_version: 3, lifecycle_version: 2,
    provided_services: [engineServiceDescriptor("wuu")],
    required_services: [requireEngineGateway()],
  }),
  async invokeService({ method, params }, host, execution) {
    if (method === "descriptor") return {
      id: "wuu", version: "1.0.0", enabled: true, requires_host_model: false,
      capabilities: ["canonical-history"],
    };
    if (method === "describe" || method === "refresh") return { binary_ok: true, display_name: label, protocol: "plugin" };
    if (method === "bind") {
      const id = `${label}-${++sequence}`;
      sessions.set(id, { binding: params as EngineBinding, closed: false });
      await observe({ phase: "bind", session: id, binding: params });
      return { session_id: id };
    }
    const id = (params as EngineSessionParams).session_id;
    const session = sessions.get(id);
    if (!session) throw new Error("external engine session unavailable");
    if (method === "close") {
      session.closed = true;
      session.controller?.abort(new Error("engine closed"));
      await session.done;
      sessions.delete(id);
      await observe({ phase: "close", session: id });
      return {};
    }
    if (method === "interrupt") { session.controller?.abort(new Error("engine interrupted")); return {}; }
    if (method !== "run") throw new Error(`unsupported external engine method ${method}`);
    if (session.closed || session.done) throw new Error("external engine is closed or already running");
    const input = params as EngineRunParams;
    const prompt = [...input.messages].reverse().find(message => message.role === "user")?.content ?? "";
    const controller = new AbortController();
    session.controller = controller;
    let drained!: () => void;
    session.done = new Promise<void>(resolve => { drained = resolve; });
    const signal = AbortSignal.any([execution.signal, controller.signal]);
    try {
      await observe({ phase: "run", session: id, prompt, binding: session.binding }, signal);
      if (String(prompt).includes("external-failure")) throw new Error("selected external engine failed");
      const content = `${label}:${prompt}`;
      const gateway = engineGateway(host, session.binding.gateway_id, { signal });
      await gateway.emit({ version: 1, type: "content_delta", content });
      return { result: { version: 1, content, new_messages: [{ role: "assistant", content }], durable_new_messages: [] } };
    } catch (error) {
      if (signal.aborted) await observe({ phase: "cancelled", session: id, prompt });
      throw error;
    } finally {
      session.controller = undefined;
      session.done = undefined;
      drained();
    }
  },
} satisfies RuntimePlugin;
