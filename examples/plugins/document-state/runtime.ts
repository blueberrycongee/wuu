import {
  documentStore, requireDocumentStore, runJSONLRuntime,
  type RuntimePlugin, type StateQuery, type StateTransaction,
} from "@wuu/plugin-sdk";

// A process consumer of the selected store. The plugin owns its document schema;
// the selected provider owns atomicity, durable receipts and recovery.
let store: ReturnType<typeof documentStore> | undefined;
const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      required_services: [requireDocumentStore()],
      tools: [{
        id: "document-state",
        description: "Read or atomically update the example document namespace.",
        input_schema: {
          type: "object",
          properties: {
            transaction: { type: "object", description: "Optional state.documents v1 transaction." },
            query: { type: "object", description: "State query to read after the commit." },
          },
          required: ["query"],
        },
      }],
    };
  },
  activate(host) { store = documentStore(host); },
  async shutdown() {
    if (!store) return;
    const view = await store.read<{ shutdowns: number }>({ namespace: "example.lifecycle", keys: ["consumer"] });
    await store.commit({
      id: crypto.randomUUID(), expected_revision: view.revision,
      writes: [{ namespace: "example.lifecycle", key: "consumer", value: { shutdowns: (view.documents[0]?.value.shutdowns ?? 0) + 1 } }],
    });
  },
  async executeTool({ arguments: args }, host) {
    const input = args as { transaction?: StateTransaction; query: StateQuery };
    const store = documentStore(host);
    const receipt = input.transaction ? await store.commit(input.transaction) : undefined;
    const view = await store.read(input.query);
    return { result: { content: [{ type: "text", text: JSON.stringify({ receipt, view }) }] } };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
