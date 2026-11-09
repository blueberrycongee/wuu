import { connect } from "node:net";
import { createInterface } from "node:readline";
import {
  createPluginSettings, requireKernelService, runJSONLRuntime,
  type RuntimePlugin, type PluginSettingsSnapshot,
} from "@wuu/plugin-sdk";

let resource: { size: unknown };
let builds = 0;
const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      required_services: [requireKernelService("host.settings.list"), requireKernelService("host.settings.get")],
      tools: [{ id: "configured-worker", description: "Synthetic configuration effectiveness acceptance worker.",
        input_schema: { type: "object", properties: { hold: { type: "boolean" } } } }],
    };
  },
  async prepare(host, lifetime) {
    const settings = createPluginSettings(host, { signal: lifetime.signal });
    const initial = await settings.capture();
    resource = { size: initial.values.resource };
    builds++;
  },
  async executeTool({ arguments: args }, host, execution) {
    const settings = createPluginSettings(host, { signal: execution.signal });
    const notifications: PluginSettingsSnapshot[] = [];
    settings.subscribe(value => notifications.push(value));
    const captured = await settings.capture();
    if ((args as { hold?: boolean }).hold) {
      await new Promise<void>((resolve, reject) => {
        const socket = connect(Number(process.argv[2]), "127.0.0.1");
        const lines = createInterface({ input: socket });
        const abort = () => { socket.destroy(); reject(execution.signal.reason); };
        execution.signal.addEventListener("abort", abort, { once: true });
        const cleanup = () => { execution.signal.removeEventListener("abort", abort); lines.close(); socket.destroy(); };
        socket.on("connect", () => socket.write("captured\n"));
        socket.on("error", error => { cleanup(); reject(error); });
        lines.once("line", () => { cleanup(); resolve(); });
      });
    }
    const refreshed = await settings.refresh();
    const legacy = await host.call("host.settings.get", { key: "resource" });
    return { result: { content: [{ type: "text", text: JSON.stringify({
      captured, refreshed, resource, builds, pid: process.pid, legacy: legacy.value, notifications: notifications.length,
    }) }] } };
  },
};
runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch(error => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
