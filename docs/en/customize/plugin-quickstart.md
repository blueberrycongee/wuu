# Agent plugin quickstart

This tutorial adds a `greet` tool to the built-in Wuu engine. The plugin runs as a Node.js process and returns a text result. It needs the Wuu CLI, Node.js 22 or later, and a Wuu source checkout matching your CLI for the SDK.

## Prepare the SDK and package

Use the SDK from the checkout rather than depending on an npm registry release. Replace the first path with your checkout's absolute path, then run these commands from the directory where you want the plugin project:

```bash
WUU_SOURCE=/absolute/path/to/wuu
npm ci --prefix "$WUU_SOURCE/packages/plugin-sdk"
npm run build --prefix "$WUU_SOURCE/packages/plugin-sdk"
wuu plugin create hello-plugin
cd hello-plugin
npm pkg set "devDependencies.@wuu/plugin-sdk=file:$WUU_SOURCE/packages/plugin-sdk"
npm install
npm install --save-dev esbuild
npm pkg set 'scripts.build=tsc --noEmit && esbuild src/index.ts --bundle --platform=node --format=esm --outfile=dist/index.js'
```

The generator creates `plugin.json`, `package.json`, `tsconfig.json`, and `src/index.ts`. Its manifest starts `node dist/index.js` using the `wuu-plugin-v1` runtime transport. The build command above bundles the SDK into that file: Wuu's package preparation excludes `node_modules`, so plain TypeScript compilation would leave a runtime dependency missing from the installed package.

## Add the tool

Replace `src/index.ts` with:

```ts
import { runJSONLRuntime, type RuntimePlugin } from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3,
      tools: [{
        id: "greet",
        description: "Return a greeting for a name",
        input_schema: {
          type: "object",
          properties: { name: { type: "string" } },
          required: ["name"],
          additionalProperties: false,
        },
        activity: { read_only: true, concurrency_safe: true, risk: "low" },
      }],
    };
  },
  executeTool(params) {
    const name = (params.arguments as { name?: unknown })?.name;
    if (params.tool_id !== "greet" || typeof name !== "string" || !name.trim()) {
      return {
        result: {
          is_error: true,
          content: [{ type: "text", text: "A non-empty name is required." }],
        },
      };
    }
    return { result: { content: [{ type: "text", text: `Hello, ${name}!` }] } };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch(error => {
  console.error(error);
  process.exitCode = 1;
});
```

`initialize` declares the model-visible description, input schema, and scheduling metadata. The host namespaces the tool ID. `executeTool` still validates its input and returns `is_error` for a tool failure. Keep stdout for the JSONL protocol; write diagnostics to stderr.

## Build and try it

```bash
npm run build
wuu plugin validate .
wuu plugin test .
wuu plugin dev .
```

`validate` checks the package. `test` starts its runtime and checks initialization, protocol negotiation, capability descriptors, and tool registration; it does not exercise `greet` or prove behavior in a conversation.

`dev` authorizes this directory, builds a candidate, and publishes a development generation. Keep it running while editing. Saves trigger another build; build or package-validation failure leaves the previously published generation in place. Refresh can be deferred while an execution owns the active generation. The command's successful publication is not proof that every desktop or runtime contribution activated successfully—inspect the plugin's actual status too.

Open a conversation using the Wuu engine and ask it to use the greeting tool for a name. Confirm the tool result says hello. This verifies the behavior that the contract test does not cover.

## Distribute the package

```bash
wuu plugin pack .
wuu plugin install ./hello-plugin-0.1.0.zip
wuu plugin approve hello-plugin
```

The current CLI stages a local package before approval enables code execution. Development-directory authorization is separate and is not distributed in the zip. Plugins run with your user authority; package validation is not a security audit. See [plugin management](plugins.md) for disabling and removing packages.

For persistent state, services, and lifecycle callbacks, continue with the [authoring reference](plugin-authoring.md). To add UI instead of a model tool, use the [desktop quickstart](desktop-plugin-quickstart.md).
