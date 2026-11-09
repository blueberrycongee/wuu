import {
  compositionService, requireCompositionService,
  type CompositionCreate, type RuntimePlugin,
} from "@wuu/plugin-sdk";

export default {
  concurrentCapabilities: ["plugin.client.request"],
  initialize: () => ({
    protocol_version: 3, lifecycle_version: 2,
    required_services: [requireCompositionService()],
    capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }],
  }),
  async invokeCapability(params, host) {
    const { method, input } = params.input as {
      method: "create" | "inspect" | "inspectSession" | "bind" | "dispose";
      input: CompositionCreate & { id: string; session: string; expected: string };
    };
    const service = compositionService(host);
    let result: unknown;
    switch (method) {
      case "create": result = await service.create(input); break;
      case "inspect": result = await service.inspect(input.id); break;
      case "inspectSession": result = await service.inspectSession(input.session); break;
      case "bind": result = await service.bindSession(input.session, input.id, input.expected); break;
      case "dispose": result = await service.dispose(input.id); break;
      default: throw new Error("unsupported composition controller operation");
    }
    return { output: { result } };
  },
} satisfies RuntimePlugin;
