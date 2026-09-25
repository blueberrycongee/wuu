import { PLUGIN_CLIENT_REQUEST_CAPABILITY, runJSONLRuntime } from "@wuu/plugin-sdk";
import { publishCandidate } from "./publish.mjs";

runJSONLRuntime({
  initialize() {
    return { protocol_version: 2, capabilities: [{ id: PLUGIN_CLIENT_REQUEST_CAPABILITY, kind: "decision", version: 1 }] };
  },
  async invokeCapability({ input }) {
    if (input?.method !== "publish") throw new Error("Unknown Git delivery action");
    return { output: { result: await publishCandidate(input.input) } };
  },
}, { input: process.stdin, output: process.stdout }).catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
