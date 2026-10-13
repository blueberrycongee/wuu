import {
  PLUGIN_CLIENT_REQUEST_CAPABILITY,
  requireKernelService,
  runJSONLRuntime,
  type CapabilityInvokeParams,
  type RuntimeHost,
  type RuntimePlugin,
} from "@wuu/plugin-sdk";

const storageScope = "workspace" as const;
const counterKey = "counter";
let requestSequence = 0;

async function initializeCounter(host: RuntimeHost): Promise<void> {
  await host.call("host.storage.compare_exchange", {
    scope: storageScope,
    key: counterKey,
    expected: null,
    value: "0",
  });
}

async function incrementCounter(host: RuntimeHost): Promise<number> {
  for (let attempt = 0; attempt < 8; attempt += 1) {
    const current = await host.call("host.storage.get", { scope: storageScope, key: counterKey });
    const previous = Number.parseInt(current.value ?? "0", 10);
    const next = Number.isFinite(previous) ? previous + 1 : 1;
    const swapped = await host.call("host.storage.compare_exchange", {
      scope: storageScope,
      key: counterKey,
      expected: current.value,
      value: String(next),
    });
    if (swapped.swapped) return next;
  }
  throw new Error("counter changed too frequently");
}

async function startSession(host: RuntimeHost, prompt: string): Promise<unknown> {
  const requestID = `stateful-example-${++requestSequence}`;
  const created = await host.call("host.session.create", {
    request_id: `${requestID}-create`,
    name: "Stateful TypeScript example",
    visibility: "plugin",
    context_source: "fresh",
  });
  return host.call("host.session.send", {
    request_id: `${requestID}-send`,
    session_id: created.session_id,
    input: { prompt },
    cause: "stateful-runtime-example.request",
  });
}

const plugin: RuntimePlugin = {
  initialize() {
    return {
      capabilities: [{ id: PLUGIN_CLIENT_REQUEST_CAPABILITY, kind: "decision", version: 1 }],
      required_services: [
        requireKernelService("host.storage.get"),
        requireKernelService("host.storage.compare_exchange"),
        requireKernelService("host.session.create"),
        requireKernelService("host.session.send"),
      ],
    };
  },
  activate(host) {
    void initializeCounter(host).catch((error: unknown) => {
      console.error(error instanceof Error ? error.message : String(error));
    });
  },
  async invokeCapability(params: CapabilityInvokeParams, host) {
    const request = params.input as { method?: unknown; input?: unknown };
    switch (request.method) {
      case "counter.increment":
        return { output: { result: { value: await incrementCounter(host) } } };
      case "session.start": {
        const input = request.input as { prompt?: unknown } | undefined;
        const prompt = typeof input?.prompt === "string" ? input.prompt.trim() : "";
        if (prompt === "") throw new Error("prompt is required");
        return { output: { result: await startSession(host, prompt) } };
      }
      default:
        throw new Error(`unknown client method ${String(request.method)}`);
    }
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
