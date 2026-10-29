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
        description: "Render an interactive explanation, calculator, diagram, or comparison in this conversation. Use version 3 with DIL-style source: an optional {@body ...} block declares state and functions, followed by JSX components. Write ordinary expressions instead of JSON expression objects. Declare version, title, fallback, then source for progressive previews.\n\nDesign the smallest view that helps answer the user's question:\n- Explanations and processes: use an SVG relationship diagram with short labels, meaningful grouping, and arrows that meet shape edges. Put longer explanation in nearby Text; avoid converting the whole explanation into cards or tables.\n- Comparisons: align comparable values in a table or use bars with a common scale. Label units and meaningful axes. Use captions for assumptions that affect interpretation.\n- Calculators and scenarios: place a few labeled controls close to the result they change. Show useful initial values. Support resetting through setters when exploration benefits from it.\n- Diagrams: design for both wide and narrow windows. Keep labels legible when SVG scales; split dense diagrams into smaller sections. Prefer a neutral palette and one accent, using semantic colors. If color encodes categories or status, provide a text legend.\n- Composition: establish hierarchy with spacing and headings. Use cards only for real groups. Do not repeat the title, add ornamental status badges, or build a dashboard when one visual will explain the answer.\n- Streaming: place useful prose and the primary visual early, keep the main explanation expanded, and put optional detail later in Disclosure. Nested content can appear before its outer container closes. Use stable keys for repeated components.\n- Actions: keep ordinary exploration local. Label continuation buttons with the specific follow-up they send. DIL.sendMessage(text) continues this conversation only from an explicit Button or Form; DIL.copy(text) copies text.\n\nState persists per artifact. No imports, network, DOM access, arbitrary scripts, or external assets. Source is interpreted with bounded computation; supported syntax and components are documented in the source field.",
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
      return { result: { is_error: true, content: [{ type: "text", text: `UI was not rendered: ${message}. Correct the source spec and retry render_ui, or provide the answer as plain text.` }] } };
    }
  },
};

export default plugin;
