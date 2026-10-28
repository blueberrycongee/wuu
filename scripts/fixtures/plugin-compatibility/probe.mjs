// This probe consumes a retained historical npm-pack payload, never current SDK
// source. Existing baseline calls must stay unchanged when new features arrive.
import { runJSONLRuntime, requireKernelService } from "./2026-10-22/sdk/dist/index.js";

let prepared, active = false;
await runJSONLRuntime({
  initialize() {
    return {
      required_services: ["host.settings.get", "host.settings.list"].map(requireKernelService),
      capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1, error_policy: "propagate" }],
    };
  },
  async prepare(host) {
    prepared = (await host.call("host.settings.get", { key: "label" })).value;
  },
  activate() { active = true; },
  async invokeCapability(_params, host) {
    if (!active) throw new Error("called before activation");
    const value = (await host.call("host.settings.get", { key: "label" })).value;
    const entries = (await host.call("host.settings.list", {})).entries;
    return { output: { prepared, value, entries, pid: process.pid } };
  },
  shutdown() { active = false; },
}, { input: process.stdin, output: process.stdout });
