import { appendFile } from "node:fs/promises";
import { join } from "node:path";
import { delegateExecutionBackendV4, runExecutionBackendV4 } from "@wuu/plugin-sdk";

// The downstream peer is an explicit installed command, not an error fallback.
// It receives the identical immutable execution-world binding. This decorator
// adds filesystem audit behavior without copying process/worker implementation.
const command = process.argv.slice(2);
if (command.length === 0) throw new Error("an explicit downstream backend command is required");
const peer = delegateExecutionBackendV4({ command, capabilities: ["process.start.v1"] });
await runExecutionBackendV4({
  ...peer,
  methods: {
    ...peer.methods,
    async execute(request, context) {
      const audited = ["read_file", "write_file", "list_files"].includes(request.call.name);
      if (audited) await appendFile(join(context.binding.root, "acceptance-fs-audit.jsonl"), JSON.stringify({
        phase: "admitted", method: request.call.name, request: context.requestId, session: context.binding.session,
        identity: context.binding.identity, arguments: request.call.arguments,
      }) + "\n");
      const result = await peer.methods.execute!(request, context);
      if (audited) await appendFile(join(context.binding.root, "acceptance-fs-audit.jsonl"), JSON.stringify({
        phase: "completed", method: request.call.name, request: context.requestId,
      }) + "\n");
      return result;
    },
  },
});
