import {
  compositionService, requireCompositionService,
  type CompositionCreate, type CompositionRun, type RuntimeHost, type RuntimePlugin,
} from "@wuu/plugin-sdk";

type Request = {
  action: "create" | "inspect" | "bind" | "run" | "dispose";
  id?: string;
  agent?: string;
  expected?: string;
  prompt?: string;
} & Partial<CompositionCreate>;

async function dispatch(host: RuntimeHost, input: Request) {
  const scopes = compositionService(host);
  switch (input.action) {
    case "create":
      if (!input.selection) throw new Error("selection is required");
      return scopes.create({ selection: input.selection, paths: input.paths, root: input.root, driver_profile: input.driver_profile });
    case "inspect": return scopes.inspect(input.id ?? "");
    case "dispose": return scopes.dispose(input.id ?? "");
    case "bind":
      if (input.expected === undefined) throw new Error("expected is required; use an empty string for the first binding");
      return scopes.bind(input.agent ?? "", input.id ?? "", input.expected);
    case "run":
      return scopes.run({ id: input.id, agent: input.agent, prompt: input.prompt ?? "" } as CompositionRun);
    default: throw new Error("Unknown composition action");
  }
}

export default {
  concurrentCapabilities: ["plugin.client.request"],
  initialize() {
    return {
      protocol_version: 3,
      required_services: [requireCompositionService()],
      capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }],
      tools: [{
        id: "composition", description: "Create an isolated harness composition, inspect it, run a trial, explicitly bind an agent, restore a previous binding, or dispose it. Runs start fresh executions; selection changes future runs only. Scope IDs belong to this conversation. Install local TypeScript files with paths and select their module IDs in selection.",
        input_schema: {
          type: "object", required: ["action"], additionalProperties: false,
          properties: {
            action: { type: "string", enum: ["create", "inspect", "bind", "run", "dispose"] },
            selection: { type: "object", description: "Public product composition configuration. Start with version:2, profile:headless; use patches to select replacements." },
            paths: { type: "array", items: { type: "string" } },
            root: { type: "string", description: "Optional shared workspace. Omit for a new empty workspace." },
            driver_profile: { type: "string" }, id: { type: "string" }, agent: { type: "string" },
            expected: { type: "string", description: "Current binding for compare-and-exchange. Empty for the first binding or to represent unbound." },
            prompt: { type: "string" },
          },
        },
        activity: { concurrency_safe: true },
      }],
    };
  },
  async executeTool(params, host) {
    const result = await dispatch(host, params.arguments as Request);
    return { result: { content: [{ type: "text", text: JSON.stringify(result) }] } };
  },
  // Developer clients use the same public API. Their scopes are separate from
  // conversation tool scopes because they carry no conversation identity.
  async invokeCapability(params, host) {
    const { method, input } = params.input as { method: Request["action"]; input: Omit<Request, "action"> };
    return { output: { result: await dispatch(host, { ...input, action: method }) } };
  },
} satisfies RuntimePlugin;
