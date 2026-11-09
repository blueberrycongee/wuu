import type { RuntimePlugin, ToolExecuteResult } from "@wuu/plugin-sdk";

interface Item {
  content: string;
  status: "pending" | "in_progress" | "completed";
}

function object(value: unknown, allowed: readonly string[]): Record<string, unknown> {
  if (value === null || value === undefined) return {};
  if (typeof value !== "object" || Array.isArray(value)) throw new Error("invalid tool arguments: expected an object");
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error(`invalid tool arguments: unknown field ${JSON.stringify(key)}`);
  }
  return value as Record<string, unknown>;
}

function string(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value !== "string") throw new Error("invalid tool arguments: expected a string");
  return value;
}

const plugin: RuntimePlugin = {
  initialize() {
    return {
      tools: [{
        id: "update_todo",
        description: "Update the current task TODO list. Provide the full TODO list every time. Keep exactly one item in_progress until all items are completed.",
        input_schema: {
          type: "object",
          properties: {
            explanation: { type: "string", description: "Optional short explanation for why the TODO list changed." },
            todos: {
              type: "array", minItems: 1, description: "Full current TODO list.",
              items: {
                type: "object",
                properties: {
                  content: { type: "string", description: "Concrete task to complete." },
                  status: { type: "string", enum: ["pending", "in_progress", "completed"] },
                },
                required: ["content", "status"], additionalProperties: false,
              },
            },
          },
          required: ["todos"], additionalProperties: false,
        },
        display: { kind: "todo", text: "Updating TODO", capability: "todo" },
      }],
    };
  },
  executeTool(call): ToolExecuteResult {
    if (call.tool_id !== "update_todo") throw new Error(`unknown TODO tool ${JSON.stringify(call.tool_id)}`);
    const input = object(call.arguments, ["explanation", "todos"]);
    string(input.explanation);
    if (input.todos != null && !Array.isArray(input.todos)) throw new Error("invalid tool arguments: todos must be an array");
    const todos = ((input.todos ?? []) as unknown[]).map((value) => {
      const item = object(value, ["content", "status"]);
      return { content: string(item.content), status: string(item.status) } as Item;
    });
    if (todos.length === 0) throw new Error("update_todo requires at least one TODO item");
    let inProgress = 0;
    let completed = 0;
    for (const [index, item] of todos.entries()) {
      if (!item.content.trim()) throw new Error(`TODO item ${index} requires content`);
      switch (item.status) {
        case "pending": break;
        case "in_progress": inProgress++; break;
        case "completed": completed++; break;
        default: throw new Error(`TODO item ${index} has invalid status ${JSON.stringify(item.status)}`);
      }
    }
    if (completed !== todos.length && inProgress !== 1) throw new Error("unfinished TODO list requires exactly one in_progress item");
    return { result: { content: [{ type: "text", text: JSON.stringify({ action: "update_todo", status: "updated", todos }) }] } };
  },
};

export default plugin;
