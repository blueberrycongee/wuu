import { createHash } from "node:crypto";
import type { PromptAssemblyRequest, PromptAssemblyResult, PromptContribution } from "@wuu/plugin-sdk";

/** Complete product policy selected through agent.prompt, never a host override. */
export function assemblePersonalPrompt(input: PromptAssemblyRequest): PromptAssemblyResult {
  if (!input || !["main", "worker"].includes(input.role)) throw new Error("unknown prompt role");
  const sections: PromptContribution[] = [];
  const add = (key: string, content: string | undefined, isStatic = true) => {
    if (content?.trim()) sections.push({ key, content, static: isStatic });
  };
  add("base", "You are Wuu, an assistant helping the user. Follow the active product's instructions and the user's requests. Use only authorized tools, preserve source provenance, and distinguish verified outcomes from assumptions. Do not infer a project or a coding role from the host environment.");
  add("tool_surface", input.surface?.SystemFragment);
  if (Object.hasOwn(input.surface?.Tools ?? {}, "tool_search")) add("deferred_tool_catalog", input.surface.DeferredToolCatalog);
  add("environment", `# Explicit environment\n${JSON.stringify({ cwd: input.cwd, date: input.date, provider: input.provider, model: input.model })}`);
  add("user_custom_prompt", input.user_prompt);
  for (const [index, instruction] of (input.instructions ?? []).entries()) {
    add(`instruction.${index}`, `# Provided instructions\n${JSON.stringify({ path: instruction.Path, source: instruction.Source, name: instruction.Name })}\n${instruction.Content}`);
  }
  add("memory_teaching", input.memory_teaching);
  add("memory_index", input.memory_index, false);
  if (input.skills?.length) add("skills", `# Explicitly provided skills\n${JSON.stringify(input.skills)}`);
  if (input.role === "worker" && input.worker) {
    add("worker", `# Assigned worker context\nWork only within this assignment and its authorization. Return results with their original provenance; this context does not grant permission for external actions.\n${JSON.stringify(input.worker)}`);
  }
  for (const contribution of input.contributions ?? []) add(contribution.key, contribution.content, contribution.static);
  return {
    content: sections.map(section => section.content).join("\n\n"),
    sections: sections.map(section => ({ key: section.key, static: section.static, bytes: Buffer.byteLength(section.content), hash: createHash("sha256").update(section.content).digest("hex") })),
  };
}
