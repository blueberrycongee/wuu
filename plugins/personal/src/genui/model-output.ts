import type { ToolRegistration, ToolResult } from "@wuu/plugin-sdk";
import { PersonalGenUI, type TrustedActor, type ToolSource } from "./service";
import { boundedJSON, exact, record } from "./schema";

const identifier = { type: "string", pattern: "^[a-zA-Z][a-zA-Z0-9_-]{0,63}$" };
const object = (properties: Record<string, unknown>, required = Object.keys(properties)) => ({ type: "object" as const, properties, required, additionalProperties: false });
const expr = { $ref: "#/$defs/expression" }, node = { $ref: "#/$defs/node" };
const scalar = object({ type: { enum: ["string", "number", "boolean"] }, initial: { type: ["string", "number", "boolean"] }, min: { type: "number" }, max: { type: "number" }, maxLength: { type: "integer", minimum: 1, maximum: 4000 }, options: { type: "array", items: { type: "string" }, maxItems: 30 } }, ["type", "initial"]);
const common = { id: identifier, visible: expr };
const definitions = {
  expression: { anyOf: [{ type: ["string", "number", "boolean"] }, object({ get: identifier }), object({ item: identifier }), object({ op: { enum: ["add", "sub", "mul", "div", "eq", "gt", "and", "or", "not", "if", "length"] }, args: { type: "array", items: expr, minItems: 1, maxItems: 3 } })] },
  scalar,
  node: { anyOf: [
    object({ ...common, type: { enum: ["stack", "row", "card"] }, children: { type: "array", items: node, maxItems: 50 } }, ["id", "type", "children"]),
    object({ ...common, type: { const: "repeat" }, field: identifier, children: { type: "array", items: node, maxItems: 50 } }, ["id", "type", "field", "children"]),
    object({ ...common, type: { const: "text" }, value: expr }, ["id", "type", "value"]),
    object({ ...common, type: { enum: ["input", "textarea", "checkbox", "slider", "select"] }, label: { type: "string" }, field: identifier, itemField: identifier }, ["id", "type", "label"]),
    object({ ...common, type: { const: "button" }, label: { type: "string" }, action: identifier, local: { enum: ["append", "remove"] }, field: identifier }, ["id", "type", "label"]),
  ] },
};
export const personalGenUITool: ToolRegistration = {
  id: "present_interactive_answer",
  description: "Present a composable interactive answer in this Personal main conversation. Use bounded stack/row/card/text, inputs, sliders, selects, checkboxes, repeat lists and buttons. Expressions are pure AST values, never code. Reference a top-level field with {get:name}; in repeat use {item:name}. Controls bind exactly one field or itemField. Persistent fields save only on save/submit; temporary fields reset on refresh. Define save/submit actions and bind buttons by action ID, or use local append/remove for lists. A checkbox never changes a PersonalTask. Submit requests a text-only explanation of actual saved state in this conversation. The entire follow-up turn has all tools disabled: it cannot generate another UI or execute tasks. No URLs, HTML, scripts, tool calls, permissions or external effects are supported. Always include a useful fallbackText. Do not use this for an ordinary clarification question.",
  input_schema: {
    ...object({ spec: object({ schemaVersion: { const: 1 }, title: { type: "string", maxLength: 160 }, fallbackText: { type: "string", maxLength: 4000 }, fields: { type: "object", maxProperties: 32, additionalProperties: { anyOf: [
      { ...scalar, properties: { ...scalar.properties, scope: { enum: ["persistent", "temporary"] } }, required: ["type", "initial", "scope"] },
      object({ type: { const: "list" }, scope: { enum: ["persistent", "temporary"] }, maxItems: { type: "integer", minimum: 1, maximum: 50 }, item: { type: "object", additionalProperties: { $ref: "#/$defs/scalar" }, minProperties: 1, maxProperties: 8 }, initial: { type: "array", items: { type: "object", additionalProperties: { type: ["string", "number", "boolean"] } }, maxItems: 50 } }),
    ] } }, tree: node, actions: { type: "array", maxItems: 8, items: object({ id: identifier, kind: { enum: ["save", "submit"] }, label: { type: "string", maxLength: 100 } }) } }) }),
    $defs: definitions,
  },
  activity: { concurrency_safe: true },
};

/** Route only the Personal model tool. Source IDs come from the persisted turn/tool call, never model arguments. */
export async function publishModelOutput(service: PersonalGenUI, input: unknown, actor: TrustedActor, source: ToolSource): Promise<ToolResult> {
  boundedJSON(input); const args = record(input); exact(args, ["spec"]);
  const surface = await service.publish({ ...source, spec: args.spec }, actor);
  return {
    content: [{ type: "text", text: surface.spec.fallbackText }],
    structured_content: { type: "personal.genui", schemaVersion: 1, surfaceId: surface.surfaceId, messageId: surface.messageId },
    meta: { personal_genui: surface },
  };
}
