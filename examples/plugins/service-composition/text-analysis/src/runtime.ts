import { runJSONLRuntime, type RuntimePlugin } from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  // This handler has no mutable state and is safe to re-enter.
  concurrentServices: ["example.text.analysis"],
  initialize() {
    return {
      protocol_version: 3,
      provided_services: [{
        name: "example.text.analysis",
        version: "1.0.0",
        methods: [{
          name: "measure",
          input_schema: "example.text.analysis.measure.input.v1",
          output_schema: "example.text.analysis.measure.output.v1",
        }],
      }],
    };
  },
  invokeService({ service, method, params }) {
    if (service !== "example.text.analysis" || method !== "measure") {
      throw new Error("unknown text analysis method");
    }
    const input = params as { text?: unknown } | undefined;
    if (typeof input?.text !== "string") throw new Error("text must be a string");
    const text = input.text.trim();
    return {
      characters: [...input.text].length,
      words: text === "" ? 0 : text.split(/\s+/u).length,
      paragraphs: text === "" ? 0 : text.split(/\n\s*\n/u).length,
    };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
