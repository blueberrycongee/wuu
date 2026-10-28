import { isAbsolute, relative, resolve } from "node:path";
import { runExecutionBackendV4 } from "@wuu/plugin-sdk";

// A complete replacement for the file capability only. There is deliberately
// no process delegation: a shell in the physical filesystem would not share
// this backend's memory-backed execution world.
const files = new Map<string, string>();
await runExecutionBackendV4({
  capabilities: [],
  initialize() {},
  methods: {
    execute(request, context) {
      context.signal.throwIfAborted();
      const input = JSON.parse(request.call.arguments || "{}");
      const path = resolve(context.binding.root, input.path ?? ".");
      const key = relative(context.binding.root, path);
      if (isAbsolute(key) || key === ".." || key.startsWith("../")) throw new Error("path is outside the memory world");
      switch (request.call.name) {
        case "write_file":
          if (request.permission_mode === "read_only") throw new Error("memory filesystem is read-only for this request");
          if (typeof input.content !== "string" || Buffer.byteLength(input.content) > 1024 * 1024) throw new Error("invalid or oversized memory file");
          files.set(key, input.content);
          return { content: [{ type: "text", text: "memory backend wrote file" }] };
        case "read_file": {
          const content = files.get(key);
          if (content === undefined) throw new Error("file is absent in the selected memory filesystem");
          return { content: [{ type: "text", text: content }] };
        }
        case "list_files": return { content: [{ type: "text", text: [...files.keys()].filter(path => path.startsWith(key)).sort().join("\n") }] };
        default: throw new Error(`capability ${request.call.name} is unavailable in the memory world`);
      }
    },
  },
});
