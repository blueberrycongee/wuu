import type { RuntimePlugin } from "@wuu/plugin-sdk";

const cronSchema = {
  type: "object" as const,
  properties: {
    action: { type: "string", enum: ["list", "add", "remove"] }, cron: { type: "string" }, prompt: { type: "string" }, title: { type: "string" }, timezone: { type: "string" },
    mode: { type: "string", enum: ["new_thread", "thread_heartbeat"] }, heartbeat_thread_id: { type: "string" }, workspace: { type: "string", enum: ["shared", "worktree"] },
    recurring: { type: "boolean" }, durable: { type: "boolean" }, id: { type: "string" },
  }, required: ["action"],
};


// This consumer owns entry points only. The selected provider owns validation,
// durable scheduling state and every background effect.
const plugin: RuntimePlugin = {
  initialize() {
    return {
      tools: [{ id: "cron", description: "Manage scheduled Agent prompts. action=list returns tasks; action=add creates a one-shot or recurring task from a five-field cron expression; action=remove deletes a task. Use new_thread for an independent visible conversation and thread_heartbeat to wake an existing conversation. Set workspace=worktree so each triggered session runs in its own git worktree instead of the shared project directory.", input_schema: cronSchema, execution_scopes: ["root" as const] }],
      required_services: [{ name: "automation.scheduler", major_version: 1, required: true }],
    };
  },
  async executeTool(call, host, execution) {
    if (!call.arguments || typeof call.arguments !== "object" || Array.isArray(call.arguments)) throw new Error("expected a JSON object");
    const input = { ...call.arguments } as Record<string, unknown>;
    if (typeof input.action !== "string") throw new Error("cron requires action=list, add, or remove");
    let value: unknown;
    const request = (method: string) => host.call("host.service.call", {
      service: "automation.scheduler", method: "request", params: { method, input },
    }, { signal: execution.signal });
    switch (input.action.trim()) {
      case "list": {
        const result = await request("automation.list") as { tasks: unknown[] };
        value = result.tasks;
        break;
      }
      case "add":
        input.heartbeat_thread_id ||= call.session_id || call.thread_id;
        value = { action: "cron", task: await request("automation.create") };
        break;
      case "remove":
        if (typeof input.id !== "string") throw new Error("automation id is required");
        input.id = input.id.trim();
        await request("automation.remove");
        value = { action: "cron", removed: input.id };
        break;
      default: throw new Error("cron requires action=list, add, or remove");
    }
    return { result: { content: [{ type: "text", text: JSON.stringify(value) }] } };
  },
};
export default plugin;
