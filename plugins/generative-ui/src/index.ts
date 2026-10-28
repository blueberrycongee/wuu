import { createHash } from "node:crypto";
import type { RuntimePlugin, ToolExecuteResult } from "@wuu/plugin-sdk";
import inputSchema from "../schema.json";
import { parseSpec } from "../../../internal/plugin/bundled/generative-ui/desktop.js";

const MIME = "application/vnd.wuu.ui+json";
const MAX_INPUT_BYTES = 128 * 1024;

const plugin: RuntimePlugin = {
  initialize() {
    return {
      tools: [{
        id: "render_ui",
        // Presentation resources must remain top-level results in PTC mode.
        direct_only: true,
        description: "Render an experimental interactive UI in the conversation. Use version 2 for a composable program: declare keyed JSON state and a body of nested layouts, text, metrics, charts, tables, controlled inputs, conditionals and repeated nodes. Expressions compute values with bounded arithmetic, comparisons, map/filter, ranges and formatting. Events can update/reset state or copy text; an explicit button or native form submission can ask the user to continue the same conversation using sendMessage. No HTML, scripts, styles, arbitrary tool execution or network requests. Every spec needs a title and useful plain-text fallback. Declare state before body for incremental previews. Version 1 text/table/chart/form specs remain supported. Arguments must fit in 128 KiB; programs have at most 256 nodes, 12 node levels, 2048 expressions, 16 expression levels and 16 KiB of initial state.",
        input_schema: { ...inputSchema, type: "object" },
        activity: { read_only: true, concurrency_safe: true },
        display: { kind: "generative-ui", label: "Render UI", text: "Rendering interactive UI", capability: "generative-ui" },
      }],
    };
  },
  executeTool(call): ToolExecuteResult {
    if (call.tool_id !== "render_ui") throw new Error(`unknown generative UI tool ${JSON.stringify(call.tool_id)}`);
    try {
      const input = call.arguments;
      if (!input || typeof input !== "object" || Array.isArray(input)
        || Object.keys(input).length !== 1 || !Object.hasOwn(input, "spec")) {
        throw new Error("Expected exactly one spec property");
      }
      // JSON.parse accepts exponent overflow as Infinity. Reject it before
      // JSON.stringify could silently coerce it to a valid nullable value.
      const argumentsText = JSON.stringify(input, (_key, value: unknown) => {
        if (typeof value === "number" && !Number.isFinite(value)) throw new Error("Invalid number");
        return value;
      });
      if (Buffer.byteLength(argumentsText) > MAX_INPUT_BYTES) throw new Error("Arguments exceed 128 KiB");
      // The same bounded validator protects new output and historical rendering.
      // It is bundled into this executable; no renderer or host-private API runs here.
      const spec = parseSpec(JSON.stringify((input as { spec: unknown }).spec));
      const text = JSON.stringify(spec);
      const digest = createHash("sha256").update(text).digest("hex");
      const identity = JSON.stringify([
        call.session_id ?? "", call.thread_id ?? "", call.turn_id ?? "",
        call.call_id, call.execution_id ?? "", digest,
      ]);
      const scope = createHash("sha256").update(identity).digest("hex");
      return { result: { content: [
        { type: "text", text: spec.fallback },
        {
          type: "resource", mime_type: MIME, name: spec.title,
          resource: { mimeType: MIME, text },
          artifact: { placement: "inline", ref: `ui-v${spec.version}-${scope}`, sha256: digest, size_bytes: Buffer.byteLength(text) },
        },
      ] } };
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      const characters = [...detail];
      const message = characters.length > 4000 ? characters.slice(0, 4000).join("") + "…" : detail;
      return { result: { is_error: true, content: [{ type: "text", text: `UI was not rendered: ${message}. Correct the version 1 or 2 spec and retry render_ui, or provide the answer as plain text.` }] } };
    }
  },
};

export default plugin;
