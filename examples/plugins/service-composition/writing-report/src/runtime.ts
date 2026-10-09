import { runJSONLRuntime, type RuntimePlugin } from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3,
      required_services: [{ name: "example.text.analysis", major_version: 1, required: true }],
      tools: [{
        id: "writing-report",
        description: "Measure a draft using the installed text analysis service. Words are whitespace-separated tokens.",
        input_schema: {
          type: "object",
          properties: { text: { type: "string", description: "The draft to measure." } },
          required: ["text"],
          additionalProperties: false,
        },
      }],
    };
  },
  async executeTool({ tool_id, arguments: args }, host) {
    if (tool_id !== "writing-report") throw new Error("unknown writing report tool");
    const input = args as { text?: unknown } | undefined;
    if (typeof input?.text !== "string") throw new Error("text must be a string");
    const result = await host.call("host.service.call", {
      service: "example.text.analysis",
      method: "measure",
      params: { text: input.text },
    }) as { characters: number; words: number; paragraphs: number };
    return {
      result: {
        content: [{
          type: "text",
          text: `${result.characters} characters, ${result.words} whitespace-separated words, ${result.paragraphs} paragraphs.`,
        }],
      },
    };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
